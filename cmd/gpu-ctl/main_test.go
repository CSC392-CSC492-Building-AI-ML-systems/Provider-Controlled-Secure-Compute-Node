package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/allowlist"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/control"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

var (
	jobImage   = allowlist.Entry{Digest: "sha256:" + strings.Repeat("a", 64), Image: "registry.example/team/job"}
	modelImage = allowlist.Entry{Digest: "sha256:" + strings.Repeat("b", 64), Image: "registry.example/team/ollama"}
)

// fakeManager accepts every offer and records what gpu-ctl asked for.
type fakeManager struct {
	mu      sync.Mutex
	state   lease.State
	active  *lease.Active
	offered []lease.Offer
}

func (manager *fakeManager) ReceiveOffer(_ context.Context, offer lease.Offer) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.offered = append(manager.offered, offer)
	manager.state = lease.StateRunning
	manager.active = &lease.Active{ID: offer.ID, ImageDigest: offer.ImageDigest, ExpiresAt: offer.ExpiresAt}
	return nil
}

func (manager *fakeManager) WaitForOffer(context.Context, string) error { return nil }

func (manager *fakeManager) Drain(context.Context) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.state = lease.StateDraining
	return nil
}

func (manager *fakeManager) Resume(context.Context) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.state = lease.StateIdle
	return nil
}

func (manager *fakeManager) Reclaim(context.Context) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.active == nil {
		return errors.New("no active workload available to reclaim")
	}
	manager.active, manager.state = nil, lease.StateIdle
	return nil
}

func (manager *fakeManager) State() lease.State {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.state == "" {
		return lease.StateIdle
	}
	return manager.state
}

func (manager *fakeManager) ActiveLease() (lease.Active, bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.active == nil {
		return lease.Active{}, false
	}
	return *manager.active, true
}

type nopCoordinator struct{}

func (nopCoordinator) ReportAccepted(context.Context, string) error { return nil }
func (nopCoordinator) ReportRejected(context.Context, string, lease.RejectionReason) error {
	return nil
}
func (nopCoordinator) ReportResult(context.Context, lease.Result) error { return nil }

// startAgent serves a fake node agent and returns its socket path.
func startAgent(t *testing.T, manager *fakeManager) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "gc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	socket := filepath.Join(directory, "agent", "control.sock")
	listener, err := control.Listen(socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	server := control.NewServer(manager, control.NewRecorder(nopCoordinator{}), []allowlist.Entry{jobImage, modelImage}, nil)
	go func() { served <- server.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		<-served
	})
	return socket
}

func gpuCtl(t *testing.T, socket string, stdin string, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), append([]string{"-socket", socket}, args...), strings.NewReader(stdin), &stdout, &stderr)
	return stdout.String() + stderr.String(), err
}

func TestImagesListsApprovedImages(t *testing.T) {
	socket := startAgent(t, &fakeManager{})
	output, err := gpuCtl(t, socket, "", "images")
	if err != nil {
		t.Fatalf("images returned unexpected error: %v", err)
	}
	for _, want := range []string{"1", jobImage.Image, jobImage.Digest, "2", modelImage.Image} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}
}

func TestRunChoosesImageByNumberDigestOrRepository(t *testing.T) {
	for _, choice := range []string{"2", modelImage.Digest, modelImage.Image} {
		manager := &fakeManager{}
		socket := startAgent(t, manager)
		output, err := gpuCtl(t, socket, "", "run", "-for", "10m", choice)
		if err != nil {
			t.Fatalf("run %s returned unexpected error: %v\n%s", choice, err, output)
		}
		if !strings.Contains(output, "Started lease local-") || !strings.Contains(output, "State:       running") {
			t.Errorf("run %s output:\n%s", choice, output)
		}
		manager.mu.Lock()
		if len(manager.offered) != 1 || manager.offered[0].ImageDigest != modelImage.Digest {
			t.Errorf("run %s offered %v, want the Ollama digest", choice, manager.offered)
		}
		manager.mu.Unlock()
	}
}

func TestRunAsksWhichImageWhenNoneGiven(t *testing.T) {
	manager := &fakeManager{}
	socket := startAgent(t, manager)
	output, err := gpuCtl(t, socket, "1\n", "run")
	if err != nil {
		t.Fatalf("run returned unexpected error: %v\n%s", err, output)
	}
	if !strings.Contains(output, "Choose an image to run [1-2]") {
		t.Errorf("output did not prompt:\n%s", output)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.offered) != 1 || manager.offered[0].ImageDigest != jobImage.Digest {
		t.Errorf("offered %v, want image 1", manager.offered)
	}
}

func TestRunRefusesBadChoices(t *testing.T) {
	socket := startAgent(t, &fakeManager{})
	for _, args := range [][]string{
		{"run", "3"},
		{"run", "0"},
		{"run", "registry.example/team/unknown"},
		{"run", "-for", "2h", "1"},
		{"run", "1", "2"},
	} {
		if _, err := gpuCtl(t, socket, "", args...); err == nil {
			t.Errorf("%v returned nil error", args)
		}
	}
	if _, err := gpuCtl(t, socket, "", "run"); err == nil {
		t.Error("run with no answer to the prompt returned nil error")
	}
}

func TestDrainResumeReclaimAndStatus(t *testing.T) {
	socket := startAgent(t, &fakeManager{})

	if output, err := gpuCtl(t, socket, "", "drain"); err != nil || !strings.Contains(output, "State:       draining") {
		t.Fatalf("drain = %v\n%s", err, output)
	}
	if output, err := gpuCtl(t, socket, "", "resume"); err != nil || !strings.Contains(output, "State:       idle") {
		t.Fatalf("resume = %v\n%s", err, output)
	}
	if _, err := gpuCtl(t, socket, "", "run", "1"); err != nil {
		t.Fatal(err)
	}
	if output, err := gpuCtl(t, socket, "", "status"); err != nil || !strings.Contains(output, "Job:         local-") {
		t.Fatalf("status = %v\n%s", err, output)
	}
	if output, err := gpuCtl(t, socket, "", "reclaim"); err != nil || !strings.Contains(output, "Job:         none") {
		t.Fatalf("reclaim = %v\n%s", err, output)
	}
	if _, err := gpuCtl(t, socket, "", "reclaim"); err == nil {
		t.Fatal("reclaim with no job returned nil error")
	}
}

func TestUsageErrors(t *testing.T) {
	socket := startAgent(t, &fakeManager{})
	for _, args := range [][]string{{}, {"explode"}, {"status", "extra"}} {
		if _, err := gpuCtl(t, socket, "", args...); err == nil {
			t.Errorf("%v returned nil error", args)
		}
	}
	if _, err := gpuCtl(t, filepath.Join(t.TempDir(), "none.sock"), "", "status"); err == nil || !strings.Contains(err.Error(), "node-agent -serve") {
		t.Errorf("status with no agent error = %v", err)
	}
}

func TestPrintStatusShowsCoordinatorView(t *testing.T) {
	var output bytes.Buffer
	printStatus(&output, control.Response{State: lease.StateIdle, CoordinatorStatus: "DRAINING"})
	if !strings.Contains(output.String(), "Coordinator: DRAINING\n") {
		t.Fatalf("status output = %q", output.String())
	}
}
