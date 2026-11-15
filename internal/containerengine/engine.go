// Package containerengine runs approved workloads using local, rootless Podman.
package containerengine

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/allowlist"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/capability"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/jobworkspace"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

// Engine resolves digests through provider policy, never through coordinator input.
type Engine struct {
	// Output optionally receives bounded container logs after exit; nil disables logging.
	Output    io.Writer
	allowlist *allowlist.Checker
	run       func(context.Context, ...string) (string, error)
	cdiReady  func(context.Context) (bool, error)
}

func New(checker *allowlist.Checker) *Engine {
	return &Engine{allowlist: checker, run: runPodman, cdiReady: detectCDIReady}
}

// Require an explicit registry and an untagged repository; disallow alternate
// transports, short-name resolution and command-line options at this boundary.
var referencePattern = regexp.MustCompile(`^(localhost|[a-z0-9]+([.-][a-z0-9]+)+)(:[0-9]+)?/[a-z0-9]+([._-][a-z0-9]+)*(/[a-z0-9]+([._-][a-z0-9]+)*)*@sha256:[a-f0-9]{64}$`)

const modelVolume = "ollama-models"

// Launch pulls the approved image, creates an isolated container, then starts it.
// A deadline limits container lifetime via conmon even if the agent exits.
// Without a deadline the maximum runtime is one hour.
func (engine *Engine) Launch(ctx context.Context, digest string) (lease.Workload, error) {
	return engine.launch(ctx, digest, nil, "512m", false)
}

// LaunchRequest runs a curated inference workload with its provider-owned
// request workspace and the larger resource policy required by Ollama.
func (engine *Engine) LaunchRequest(
	ctx context.Context,
	digest string,
	request *lease.InferenceRequest,
) (lease.Workload, error) {
	if request == nil {
		return nil, errors.New("inference request is required")
	}
	workspace, err := jobworkspace.New("")
	if err != nil {
		return nil, err
	}
	if err := workspace.WriteRequest(*request); err != nil {
		_ = workspace.Remove()
		return nil, err
	}
	return engine.launch(ctx, digest, workspace, "2g", request.RequireGPU)
}

func (engine *Engine) launch(
	ctx context.Context,
	digest string,
	workspace *jobworkspace.Workspace,
	memory string,
	requireGPU bool,
) (lease.Workload, error) {
	retainWorkspace := false
	defer func() {
		if !retainWorkspace && workspace != nil {
			_ = workspace.Remove()
		}
	}()
	if engine.allowlist == nil {
		return nil, errors.New("image allowlist is required")
	}
	reference, err := engine.allowlist.ImageReference(ctx, digest)
	if err != nil {
		return nil, err
	}
	if !referencePattern.MatchString(reference) {
		return nil, fmt.Errorf("image must use an explicit registry and untagged repository: %q", reference)
	}
	setupCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if requireGPU {
		if engine.cdiReady == nil {
			return nil, errors.New("refusing to launch: CDI detection is not configured")
		}
		available, err := engine.cdiReady(setupCtx)
		if err != nil {
			return nil, fmt.Errorf("check NVIDIA CDI availability: %w", err)
		}
		if !available {
			return nil, errors.New("refusing to launch: NVIDIA CDI device is unavailable")
		}
	}
	rootless, err := engine.run(setupCtx, "info", "--format", "{{.Host.Security.Rootless}}")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(rootless) != "true" {
		return nil, errors.New("refusing to launch: local Podman must be rootless")
	}
	if _, err := engine.run(setupCtx, "pull", "--tls-verify=true", "--", reference); err != nil {
		return nil, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, fmt.Errorf("generate container name: %w", err)
	}
	workload := &Workload{
		engine:    engine,
		name:      "compute-node-" + hex.EncodeToString(random[:]),
		workspace: workspace,
	}
	lifetime := time.Hour
	if deadline, ok := ctx.Deadline(); ok {
		lifetime = min(lifetime, time.Until(deadline))
	}
	if lifetime <= 0 {
		return nil, context.DeadlineExceeded
	}
	seconds := max(1, int(lifetime.Seconds()))
	args := []string{"create",
		"--name=" + workload.name, "--pull=never", "--restart=no",
		"--userns=nomap", "--user=65534:65534", "--cap-drop=ALL",
		"--security-opt=no-new-privileges", "--network=none",
		"--pid=private", "--ipc=private", "--uts=private",
		"--read-only", "--read-only-tmpfs=false", "--image-volume=ignore",
		"--tmpfs=/tmp:rw,noexec,nosuid,nodev,size=64m,mode=1777",
		"--pids-limit=128", "--cpus=1", "--memory=" + memory, "--memory-swap=" + memory,
		"--ulimit=nofile=1024:1024", "--http-proxy=false",
		"--health-cmd=none", "--timeout=" + strconv.Itoa(seconds)}
	if workspace != nil {
		args = append(args,
			"--volume="+modelVolume+":/models:ro",
			"--volume="+workspace.InputDir()+":/job/input:ro",
			"--volume="+workspace.OutputDir()+":/job/output:rw",
		)
	}
	if requireGPU {
		args = append(args, "--device=nvidia.com/gpu=all", "--env=OLLAMA_JOB_REQUIRE_GPU=1")
	}
	if engine.Output == nil {
		args = append(args, "--log-driver=none")
	} else {
		args = append(args, "--log-driver=k8s-file", "--log-opt=max-size=64kb")
	}
	args = append(args, "--", reference)
	_, err = engine.run(setupCtx, args...)
	if err == nil {
		_, err = engine.run(setupCtx, "start", "--", workload.name)
	}
	if err != nil {
		if cleanupErr := workload.cleanup(); cleanupErr != nil {
			// Preserve the handle so callers can retry cleanup and keep the
			// node reserved while container ownership remains uncertain.
			return workload, errors.Join(err, cleanupErr)
		}
		return nil, err
	}
	retainWorkspace = true
	return workload, nil
}

// Workload owns a named container. Exit metadata is retained until Wait or Kill.
type Workload struct {
	engine    *Engine
	name      string
	workspace *jobworkspace.Workspace
}

// Wait returns the container's exit code (including nonzero job exits), then
// removes it. Cancellation also removes the container, using a fresh context.
func (workload *Workload) Wait(ctx context.Context) (int, error) {
	output, err := workload.engine.run(ctx, "wait", "--", workload.name)
	if err != nil {
		return 0, errors.Join(err, workload.cleanup())
	}
	code, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil || code < 0 || code > 255 {
		return 0, errors.Join(fmt.Errorf("invalid Podman exit code %q", output), workload.cleanup())
	}
	if workload.engine.Output != nil {
		logs, logErr := workload.engine.run(ctx, "logs", "--", workload.name)
		if logErr == nil {
			_, logErr = io.WriteString(workload.engine.Output, logs)
		}
		return code, errors.Join(logErr, workload.cleanup())
	}
	return code, workload.cleanup()
}

// Kill force-removes only this workload; repeat calls are harmless.
func (workload *Workload) Kill(ctx context.Context) error {
	_, err := workload.engine.run(ctx, "rm", "--force", "--ignore", "--time=0", "--", workload.name)
	if err == nil && workload.workspace != nil {
		err = workload.workspace.Remove()
	}
	return err
}

func (workload *Workload) cleanup() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return workload.Kill(ctx)
}

// Keep diagnostics bounded even if Podman emits a large pull response.
type limitedBuffer struct{ buffer bytes.Buffer }

func (buffer *limitedBuffer) Len() int       { return buffer.buffer.Len() }
func (buffer *limitedBuffer) String() string { return buffer.buffer.String() }

func (buffer *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 16384 - buffer.Len(); remaining > 0 {
		_, _ = buffer.buffer.Write(p[:min(len(p), remaining)])
	}
	return n, nil
}

func runPodman(ctx context.Context, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "podman", append([]string{"--remote=false"}, args...)...)
	var stdout, stderr limitedBuffer
	command.Stdout, command.Stderr = &stdout, &stderr
	// Bound waits if a child process inherits the command's output pipes.
	command.WaitDelay = time.Second
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("podman %s: %w: %s", args[0], errors.Join(ctx.Err(), err), strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func detectCDIReady(ctx context.Context) (bool, error) {
	runtimes, err := capability.DetectRuntimes(ctx)
	if err != nil {
		return false, err
	}
	return runtimes.CDI.Available, nil
}
