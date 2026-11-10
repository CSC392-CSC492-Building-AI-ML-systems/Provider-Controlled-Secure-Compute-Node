package containerengine

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/allowlist"
)

var testDigest = "sha256:" + strings.Repeat("a", 64)

func testEngine(t *testing.T) *Engine {
	t.Helper()
	checker, err := allowlist.NewChecker([]allowlist.Entry{{Digest: testDigest, Image: "registry.example/team/job"}})
	if err != nil {
		t.Fatal(err)
	}
	return New(checker)
}

func TestLaunchIsolationAndWait(t *testing.T) {
	engine := testEngine(t)
	var calls [][]string
	engine.run = func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, args)
		switch args[0] {
		case "info":
			return "true\n", nil
		case "wait":
			return "42\n", nil
		}
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	workload, err := engine.Launch(ctx, testDigest)
	if err != nil {
		t.Fatal(err)
	}
	if code, err := workload.Wait(ctx); err != nil || code != 42 {
		t.Fatalf("Wait = %d, %v", code, err)
	}
	var stages []string
	for _, call := range calls {
		stages = append(stages, call[0])
	}
	if !reflect.DeepEqual(stages, []string{"info", "pull", "create", "start", "wait", "rm"}) {
		t.Fatalf("stages = %v", stages)
	}
	reference := "registry.example/team/job@" + testDigest
	if !reflect.DeepEqual(calls[1], []string{"pull", "--tls-verify=true", "--", reference}) {
		t.Fatalf("pull = %v", calls[1])
	}
	create := calls[2]
	for _, flag := range []string{
		"--pull=never", "--restart=no", "--userns=nomap", "--user=65534:65534",
		"--cap-drop=ALL", "--security-opt=no-new-privileges", "--network=none",
		"--pid=private", "--ipc=private", "--uts=private", "--read-only",
		"--read-only-tmpfs=false", "--image-volume=ignore",
		"--tmpfs=/tmp:rw,noexec,nosuid,nodev,size=64m,mode=1777", "--pids-limit=128",
		"--cpus=1", "--memory=512m", "--memory-swap=512m", "--ulimit=nofile=1024:1024",
		"--log-driver=none", "--http-proxy=false", "--health-cmd=none",
	} {
		found := false
		for _, arg := range create {
			if arg == flag {
				found = true
			}
		}
		if !found {
			t.Errorf("missing isolation flag %s", flag)
		}
	}
	if create[len(create)-1] != reference || create[len(create)-2] != "--" {
		t.Fatalf("create reference = %v", create)
	}
	name := strings.TrimPrefix(create[1], "--name=")
	if !reflect.DeepEqual(calls[5], []string{"rm", "--force", "--ignore", "--time=0", "--", name}) {
		t.Fatalf("cleanup = %v", calls[5])
	}
}

func TestLaunchFailures(t *testing.T) {
	failure := errors.New("runtime failed")
	for _, stage := range []string{"info", "rootful", "pull", "create", "start", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			engine := testEngine(t)
			var calls []string
			engine.run = func(_ context.Context, args ...string) (string, error) {
				calls = append(calls, args[0])
				if args[0] == stage || stage == "cleanup" && (args[0] == "start" || args[0] == "rm") {
					return "", failure
				}
				if args[0] == "info" {
					if stage == "rootful" {
						return "false", nil
					}
					return "true", nil
				}
				return "", nil
			}
			workload, err := engine.Launch(context.Background(), testDigest)
			if err == nil {
				t.Fatal("expected launch failure")
			}
			if stage == "cleanup" && workload == nil {
				t.Fatal("lost container handle after cleanup failure")
			}
			if stage != "cleanup" && workload != nil {
				t.Fatal("unexpected workload after failure")
			}
			if stage == "create" || stage == "start" || stage == "cleanup" {
				if calls[len(calls)-1] != "rm" {
					t.Fatalf("no cleanup: %v", calls)
				}
			} else {
				for _, call := range calls {
					if call == "create" {
						t.Fatal("created container after failed preflight/pull")
					}
				}
			}
		})
	}
}

func TestRejectUnsafeReferencesBeforePodman(t *testing.T) {
	for _, image := range []string{"ubuntu", "docker.io/library/ubuntu:latest", "dir:/tmp/image", "--privileged", "docker://registry.example/job"} {
		t.Run(image, func(t *testing.T) {
			checker, err := allowlist.NewChecker([]allowlist.Entry{{Digest: testDigest, Image: image}})
			if err != nil {
				t.Fatal(err)
			}
			engine := New(checker)
			engine.run = func(context.Context, ...string) (string, error) {
				t.Fatal("called Podman for invalid reference")
				return "", nil
			}
			if _, err := engine.Launch(context.Background(), testDigest); err == nil {
				t.Fatal("accepted unsafe reference")
			}
		})
	}
	engine := testEngine(t)
	engine.run = func(context.Context, ...string) (string, error) {
		t.Fatal("called Podman for unapproved digest")
		return "", nil
	}
	if _, err := engine.Launch(context.Background(), "sha256:"+strings.Repeat("b", 64)); !errors.Is(err, allowlist.ErrNotAllowed) {
		t.Fatalf("error = %v", err)
	}
}

func TestWaitCancellationAndMalformedExitCleanUp(t *testing.T) {
	for _, output := range []string{"cancelled", "not-a-code", "256"} {
		t.Run(output, func(t *testing.T) {
			engine := testEngine(t)
			removed := false
			engine.run = func(ctx context.Context, args ...string) (string, error) {
				if args[0] == "wait" {
					if output == "cancelled" {
						return "", context.Canceled
					}
					return output, nil
				}
				if args[0] != "rm" || ctx.Err() != nil {
					t.Fatalf("invalid cleanup: %v, %v", args, ctx.Err())
				}
				removed = true
				return "", nil
			}
			workload := &Workload{engine: engine, name: "owned-container"}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := workload.Wait(ctx); err == nil || !removed {
				t.Fatalf("Wait error=%v, removed=%v", err, removed)
			}
		})
	}
}

func TestPodmanCommandUsesLocalModeAndPreservesCancellation(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\n[ \"$1\" = '--remote=false' ] || exit 99\ncase \"$2\" in\ninfo) echo true;;\nfail) echo failure-detail >&2; exit 125;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "podman"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if output, err := runPodman(context.Background(), "info"); err != nil || strings.TrimSpace(output) != "true" {
		t.Fatalf("info=%q, %v", output, err)
	}
	if _, err := runPodman(context.Background(), "fail"); err == nil || !strings.Contains(err.Error(), "failure-detail") {
		t.Fatalf("error=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runPodman(ctx, "info"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	var buffer limitedBuffer
	_, _ = io.Copy(&buffer, io.LimitReader(strings.NewReader(strings.Repeat("x", 32000)), 32000))
	if buffer.Len() != 16384 {
		t.Fatalf("diagnostic length=%d", buffer.Len())
	}
}

func TestOptionalContainerOutput(t *testing.T) {
	engine := testEngine(t)
	var output strings.Builder
	engine.Output = &output
	var calls [][]string
	engine.run = func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, args)
		switch args[0] {
		case "info":
			return "true", nil
		case "wait":
			return "0", nil
		case "logs":
			return "I'm in a podman container!\n", nil
		}
		return "", nil
	}
	workload, err := engine.Launch(context.Background(), testDigest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workload.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if output.String() != "I'm in a podman container!\n" {
		t.Fatalf("output = %q", output.String())
	}
	create := strings.Join(calls[2], " ")
	if !strings.Contains(create, "--log-driver=k8s-file") || !strings.Contains(create, "--log-opt=max-size=64kb") {
		t.Fatalf("unbounded logging: %s", create)
	}
	if calls[len(calls)-1][0] != "rm" {
		t.Fatal("logs prevented cleanup")
	}
}
