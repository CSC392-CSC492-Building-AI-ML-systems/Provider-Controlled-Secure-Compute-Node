package main

import (
	"bytes"
	"context"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/agent"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/control"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/coordinator"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

// lockedBuffer is a log destination that tests can read while the agent writes.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestServeStopsNewWorkWhileCoordinatorIsLost(t *testing.T) {
	dir := t.TempDir()
	digest := "sha256:" + strings.Repeat("b", 64)
	policy := filepath.Join(dir, "allowlist.json")
	if err := os.WriteFile(policy, []byte(`{"images":[{"digest":"`+digest+`","image":"registry.example/team/job"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "podman"), []byte(fakeServePodman), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/bin:/usr/bin")
	t.Setenv("PODMAN_TEST_DIR", dir)
	t.Setenv("PODMAN_TEST_JOB", "finish")

	// The fake coordinator answers registration and heartbeats only while up.
	var up atomic.Bool
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/providers" {
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{}`)
			return
		}
		io.WriteString(w, `{"status":"ACTIVE"}`)
	}))
	defer fake.Close()

	socketDir, err := os.MkdirTemp("", "na")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(socketDir)
	socket := filepath.Join(socketDir, "agent", "control.sock")

	heartbeat := &agent.Heartbeat{
		Client:    coordinator.NewP16(fake.URL, "node-1", ""),
		Interval:  10 * time.Millisecond,
		LostAfter: 50 * time.Millisecond,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	var logs lockedBuffer
	var events bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() {
		served <- serveWithHeartbeat(ctx, policy, socket, &events, nil, log.New(&logs, "", 0), heartbeat, false)
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
				t.Fatalf("timed out waiting for %s; agent log:\n%s", what, logs.String())
			}
		}
	}
	// tryRun asks for a job and returns the control error, if any.
	tryRun := func() (control.Response, error) {
		requestCtx, requestCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer requestCancel()
		return control.Send(requestCtx, socket, control.Request{Command: control.CommandRun, Digest: digest})
	}
	runRejected := func(when string) {
		t.Helper()
		_, err := tryRun()
		if err == nil || !strings.Contains(err.Error(), string(lease.RejectionDisconnected)) {
			t.Fatalf("run %s returned %v, want a disconnected rejection", when, err)
		}
	}
	runCompletes := func(when string) {
		t.Helper()
		response := send(control.Request{Command: control.CommandRun, Digest: digest})
		if !response.OK {
			t.Fatalf("run %s failed: %s", when, response.Error)
		}
		eventually("job "+when+" to complete", func() bool {
			last := send(control.Request{Command: control.CommandStatus}).LastResult
			return last != nil && last.LeaseID == response.LeaseID && last.Outcome == lease.OutcomeCompleted
		})
	}

	eventually("agent to listen", func() bool {
		_, err := control.Send(context.Background(), socket, control.Request{Command: control.CommandStatus})
		return err == nil
	})

	// Not registered yet: no new work.
	runRejected("before registration")

	up.Store(true)
	eventually("registration", func() bool {
		_, err := tryRun()
		return err == nil
	})
	eventually("first job to finish", func() bool {
		return send(control.Request{Command: control.CommandStatus}).State == lease.StateIdle
	})

	// Coordinator goes away for longer than LostAfter.
	up.Store(false)
	eventually("coordinator to be declared lost", func() bool {
		return strings.Contains(logs.String(), "coordinator lost")
	})
	runRejected("while coordinator is lost")

	// It comes back, and the node takes work again.
	up.Store(true)
	eventually("coordinator to recover", func() bool {
		return strings.Contains(logs.String(), "coordinator reachable again")
	})
	runCompletes("after recovery")

	cancel()
	if err := <-served; err != nil {
		t.Fatalf("serve returned unexpected error: %v", err)
	}
}
