package main

import (
	"bytes"
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/control"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

// fakeServePodman behaves like rootless Podman for one job. With
// PODMAN_TEST_JOB=finish the job exits 0 at once; otherwise "wait" blocks
// until the container is removed, like a long-running job being reclaimed.
const fakeServePodman = `#!/bin/sh
[ "$1" = '--remote=false' ] || exit 99
printf '%s\n' "$2" >> "$PODMAN_TEST_DIR/calls"
case "$2" in
info) echo true;;
create) rm -f "$PODMAN_TEST_DIR/removed";;
wait) if [ "$PODMAN_TEST_JOB" = finish ]; then echo 0; exit 0; fi
      while [ ! -f "$PODMAN_TEST_DIR/removed" ]; do sleep 0.02; done
      echo 'Error: no such container' >&2; exit 125;;
rm) touch "$PODMAN_TEST_DIR/removed";;
esac
`

func TestServeRunsReclaimsAndCompletesJobs(t *testing.T) {
	dir := t.TempDir()
	digest := "sha256:" + strings.Repeat("a", 64)
	policy := filepath.Join(dir, "allowlist.json")
	if err := os.WriteFile(policy, []byte(`{"images":[{"digest":"`+digest+`","image":"registry.example/team/job"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "podman"), []byte(fakeServePodman), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/bin:/usr/bin")
	t.Setenv("PODMAN_TEST_DIR", dir)

	socketDir, err := os.MkdirTemp("", "na")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(socketDir)
	socket := filepath.Join(socketDir, "agent", "control.sock")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var events bytes.Buffer
	served := make(chan error, 1)
	go func() {
		served <- serve(ctx, policy, socket, &events, nil, log.New(io.Discard, "", 0))
	}()

	send := func(request control.Request) control.Response {
		t.Helper()
		requestCtx, requestCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer requestCancel()
		response, err := control.Send(requestCtx, socket, request)
		if err != nil {
			t.Fatalf("%s returned unexpected error: %v", request.Command, err)
		}
		return response
	}
	eventually := func(what string, condition func() bool) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); !condition(); time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
		}
	}
	eventually("agent to listen", func() bool {
		_, err := control.Send(context.Background(), socket, control.Request{Command: control.CommandStatus})
		return err == nil
	})

	// A long-running job is reclaimed while the agent is waiting on it.
	started := send(control.Request{Command: control.CommandRun, Digest: digest})
	eventually("job to run", func() bool {
		calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
		return strings.Contains(string(calls), "wait")
	})
	reclaimed := send(control.Request{Command: control.CommandReclaim})
	if reclaimed.State != lease.StateIdle || reclaimed.Lease != nil {
		t.Fatalf("status after reclaim = %+v", reclaimed)
	}
	if last := reclaimed.LastResult; last == nil || last.LeaseID != started.LeaseID || last.Outcome != lease.OutcomeReclaimed {
		t.Fatalf("last result after reclaim = %+v", last)
	}

	// The node takes the next job, which runs to completion.
	t.Setenv("PODMAN_TEST_JOB", "finish")
	second := send(control.Request{Command: control.CommandRun, Digest: digest})
	eventually("second job to complete", func() bool {
		last := send(control.Request{Command: control.CommandStatus}).LastResult
		return last != nil && last.LeaseID == second.LeaseID && last.Outcome == lease.OutcomeCompleted
	})

	cancel()
	if err := <-served; err != nil {
		t.Fatalf("serve returned unexpected error: %v", err)
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Errorf("control socket left behind after shutdown: %v", err)
	}
	lines := strings.Count(events.String(), "\n")
	if lines != 4 || strings.Count(events.String(), `"type":"accepted"`) != 2 ||
		!strings.Contains(events.String(), `"outcome":"reclaimed"`) ||
		!strings.Contains(events.String(), `"outcome":"completed"`) {
		t.Fatalf("events:\n%s", events.String())
	}
}
