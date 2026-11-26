package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/agent"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/config"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/control"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/dashboard"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

// fakeP16 is a coordinator that answers registration and heartbeats.
type fakeP16 struct {
	mu            sync.Mutex
	down          bool // registration fails with 503 while set
	registerCode  int  // non-zero: reject registration with this status
	registered    map[string]any
	heartbeats    int
	heartbeatCode int
	status        string
}

func (f *fakeP16) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == "POST" && r.URL.Path == "/providers":
		if f.down {
			w.WriteHeader(503)
			return
		}
		if f.registerCode != 0 {
			w.WriteHeader(f.registerCode)
			w.Write([]byte(`{"error":{"code":"VALIDATION_ERROR","message":"bad"}}`))
			return
		}
		json.NewDecoder(r.Body).Decode(&f.registered)
		w.WriteHeader(201)
		w.Write([]byte(`{"provider_id":"n1","status":"ACTIVE"}`))
	case r.Method == "POST" && r.URL.Path == "/providers/n1/heartbeat":
		f.heartbeats++
		if f.heartbeatCode != 0 {
			w.WriteHeader(f.heartbeatCode)
			return
		}
		status := f.status
		if status == "" {
			status = "ACTIVE"
		}
		json.NewEncoder(w).Encode(map[string]string{"provider_id": "n1", "status": status})
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeP16) with(fn func(*fakeP16)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func gpuReport(context.Context) dashboard.CapabilityReport {
	return dashboard.CapabilityReport{
		GPUs:     []dashboard.GPU{{Name: "NVIDIA GeForce RTX 4080", VRAMTotalMB: 16376, UUID: "GPU-x"}},
		Driver:   dashboard.DriverInfo{Version: "580.178.04"},
		Runtimes: dashboard.Runtimes{Podman: dashboard.PodmanInfo{Installed: true, Rootless: true}},
	}
}

const testDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// federatedConfig writes an allowlist and returns a config pointing at
// coordinatorURL, a short control socket path, and installs report as the
// capability discovery for the test.
func federatedConfig(t *testing.T, coordinatorURL string, report discoverFunc) (config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	policy := filepath.Join(dir, "allowlist.json")
	if err := os.WriteFile(policy, []byte(`{"images":[{"digest":"`+testDigest+`","image":"registry.example/team/job"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	socketDir, err := os.MkdirTemp("", "na")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(socketDir) })
	saved := discover
	discover = report
	t.Cleanup(func() { discover = saved })
	return config.Config{
		AllowlistPath:     policy,
		CoordinatorURL:    coordinatorURL,
		ProviderID:        "n1",
		AcceptedTiers:     []string{"standard"},
		Runtimes:          []string{"ollama"},
		HeartbeatInterval: config.Duration(10 * time.Millisecond),
		LostAfter:         config.Duration(time.Second),
	}, filepath.Join(socketDir, "control.sock")
}

func eventually(t *testing.T, what string, condition func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !condition(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func TestServeRegistersBeforeTakingOffersThenHeartbeats(t *testing.T) {
	coord := &fakeP16{down: true}
	srv := httptest.NewServer(coord)
	defer srv.Close()
	cfg, socket := federatedConfig(t, srv.URL, gpuReport)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- serve(ctx, cfg, socket, io.Discard, nil, log.New(io.Discard, "", 0), serveOptions{}) }()
	eventually(t, "agent to listen", func() bool {
		_, err := control.Send(context.Background(), socket, control.Request{Command: control.CommandStatus})
		return err == nil
	})

	// The coordinator is down, so the node is not registered and refuses offers (R1).
	_, err := control.Send(context.Background(), socket, control.Request{Command: control.CommandRun, Digest: testDigest})
	if err == nil || !strings.Contains(err.Error(), string(lease.RejectionDisconnected)) {
		t.Fatalf("run before registration: err=%v, want a %q rejection", err, lease.RejectionDisconnected)
	}

	coord.with(func(f *fakeP16) { f.down = false })
	eventually(t, "registration and heartbeats", func() bool {
		var beats int
		coord.with(func(f *fakeP16) { beats = f.heartbeats })
		return beats >= 2
	})
	coord.with(func(f *fakeP16) {
		if f.registered["provider_id"] != "n1" {
			t.Errorf("registered provider_id = %v", f.registered["provider_id"])
		}
		caps := f.registered["capabilities"].(map[string]any)
		if caps["gpu_model"] != "NVIDIA GeForce RTX 4080" || caps["vram_mb"] != float64(16376) ||
			caps["driver_version"] != "580.178.04" || len(caps) != 4 {
			t.Errorf("registered capabilities = %v", caps)
		}
		if runtimes := caps["runtimes"].([]any); len(runtimes) != 2 || runtimes[0] != "ollama" || runtimes[1] != "podman" {
			t.Errorf("registered runtimes = %v", runtimes)
		}
		if tiers := f.registered["accepted_tiers"].([]any); len(tiers) != 1 || tiers[0] != "standard" {
			t.Errorf("registered tiers = %v", tiers)
		}
	})

	cancel()
	if err := <-served; err != nil {
		t.Fatalf("serve returned %v", err)
	}
}

func TestServeRefusesWithoutGPU(t *testing.T) {
	coord := &fakeP16{}
	srv := httptest.NewServer(coord)
	defer srv.Close()
	cpuOnly := func(context.Context) dashboard.CapabilityReport {
		return dashboard.CapabilityReport{
			Runtimes: dashboard.Runtimes{Podman: dashboard.PodmanInfo{Installed: true, Rootless: true}},
			Host:     dashboard.HostInfo{RAMTotalMB: 8192},
		}
	}
	cfg, socket := federatedConfig(t, srv.URL, cpuOnly)

	err := serve(context.Background(), cfg, socket, io.Discard, nil, log.New(io.Discard, "", 0), serveOptions{})
	if !errors.Is(err, agent.ErrNoGPU) {
		t.Fatalf("serve() = %v, want ErrNoGPU", err)
	}
	if _, statErr := os.Stat(socket); !os.IsNotExist(statErr) {
		t.Fatalf("control socket created by a node that refused to start: %v", statErr)
	}

	// With cpu_only the same machine registers using its RAM.
	cfg.CPUOnly = true
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- serve(ctx, cfg, socket, io.Discard, nil, log.New(io.Discard, "", 0), serveOptions{}) }()
	eventually(t, "cpu-only registration", func() bool {
		var caps map[string]any
		coord.with(func(f *fakeP16) { caps, _ = f.registered["capabilities"].(map[string]any) })
		return caps != nil && caps["gpu_model"] == "cpu" && caps["vram_mb"] == float64(8192)
	})
	cancel()
	if err := <-served; err != nil {
		t.Fatalf("serve returned %v", err)
	}
}

func TestServeStopsWhenRegistrationRejected(t *testing.T) {
	srv := httptest.NewServer(&fakeP16{registerCode: 422})
	defer srv.Close()
	cfg, socket := federatedConfig(t, srv.URL, gpuReport)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := serve(ctx, cfg, socket, io.Discard, nil, log.New(io.Discard, "", 0), serveOptions{})
	if err == nil || !strings.Contains(err.Error(), "VALIDATION_ERROR") {
		t.Fatalf("serve() = %v, want the rejected registration", err)
	}
	if ctx.Err() != nil {
		t.Fatal("serve kept running after registration was rejected")
	}
}

func TestServeHandlesHeartbeatResults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "podman"), []byte(fakeServePodman), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/bin:/usr/bin")
	t.Setenv("PODMAN_TEST_DIR", dir)

	coord := &fakeP16{}
	srv := httptest.NewServer(coord)
	defer srv.Close()
	cfg, socket := federatedConfig(t, srv.URL, gpuReport)
	cfg.LostAfter = config.Duration(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	served := make(chan error, 1)
	go func() { served <- serve(ctx, cfg, socket, io.Discard, nil, log.New(io.Discard, "", 0), serveOptions{}) }()
	defer func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("serve returned %v", err)
		}
	}()

	send := func(command string) (control.Response, error) {
		return control.Send(ctx, socket, control.Request{Command: command, Digest: testDigest})
	}
	wantStatus := func(status string) {
		t.Helper()
		eventually(t, "coordinator status "+status, func() bool {
			reply, err := send(control.CommandStatus)
			return err == nil && string(reply.CoordinatorStatus) == status
		})
	}
	start := func() string {
		t.Helper()
		reply, err := send(control.CommandRun)
		if err != nil {
			t.Fatal(err)
		}
		return reply.LeaseID
	}
	wantReclaimed := func(id string) {
		t.Helper()
		eventually(t, "lease "+id+" to be reclaimed", func() bool {
			reply, err := send(control.CommandStatus)
			return err == nil && reply.Lease == nil && reply.LastResult != nil &&
				reply.LastResult.LeaseID == id && reply.LastResult.Outcome == lease.OutcomeReclaimed
		})
	}
	wantDisconnected := func() {
		t.Helper()
		if _, err := send(control.CommandRun); err == nil || !strings.Contains(err.Error(), string(lease.RejectionDisconnected)) {
			t.Fatalf("run = %v, want disconnected rejection", err)
		}
	}

	wantStatus("ACTIVE")
	first := start()
	coord.with(func(f *fakeP16) { f.status = "DRAINING" })
	wantStatus("DRAINING")
	coord.with(func(f *fakeP16) { f.heartbeatCode = http.StatusServiceUnavailable })
	wantReclaimed(first)
	wantDisconnected()

	coord.with(func(f *fakeP16) {
		f.heartbeatCode = 0
		f.status = "ACTIVE"
	})
	eventually(t, "lease admission to recover", func() bool {
		reply, err := send(control.CommandRun)
		if err == nil {
			first = reply.LeaseID
			return true
		}
		if !strings.Contains(err.Error(), string(lease.RejectionDisconnected)) {
			t.Fatal(err)
		}
		return false
	})
	wantStatus("ACTIVE")
	coord.with(func(f *fakeP16) { f.heartbeatCode = http.StatusConflict })
	wantStatus("DRAINED")
	wantReclaimed(first)
	if _, err := send(control.CommandResume); err != nil {
		t.Fatal(err)
	}
	wantDisconnected()
	var stoppedAt int
	coord.with(func(f *fakeP16) { stoppedAt = f.heartbeats })
	time.Sleep(3 * time.Duration(cfg.HeartbeatInterval))
	coord.with(func(f *fakeP16) {
		if f.heartbeats != stoppedAt {
			t.Errorf("heartbeats continued after removal: %d -> %d", stoppedAt, f.heartbeats)
		}
	})
}
