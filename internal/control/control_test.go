package control

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/allowlist"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

var testImage = allowlist.Entry{Digest: "sha256:" + strings.Repeat("a", 64), Image: "registry.example/team/job"}

// fakeManager records commands and lets tests choose the node's answers.
type fakeManager struct {
	mu        sync.Mutex
	state     lease.State
	active    *lease.Active
	offers    []lease.Offer
	rejectErr error
	waits     chan struct{}
	commands  []string
}

func newFakeManager() *fakeManager {
	return &fakeManager{state: lease.StateIdle, waits: make(chan struct{}, 10)}
}

func (manager *fakeManager) record(command string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.commands = append(manager.commands, command)
}

func (manager *fakeManager) ReceiveOffer(_ context.Context, offer lease.Offer) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.offers = append(manager.offers, offer)
	if manager.rejectErr != nil {
		return manager.rejectErr
	}
	manager.state = lease.StateRunning
	manager.active = &lease.Active{ID: offer.ID, ImageDigest: offer.ImageDigest, ExpiresAt: offer.ExpiresAt}
	return nil
}

func (manager *fakeManager) Wait(context.Context) error {
	manager.waits <- struct{}{}
	return nil
}

func (manager *fakeManager) Drain(context.Context) error {
	manager.record("drain")
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.state = lease.StateDraining
	return nil
}

func (manager *fakeManager) Resume(context.Context) error {
	manager.record("resume")
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.state = lease.StateIdle
	return nil
}

func (manager *fakeManager) Reclaim(context.Context) error {
	manager.record("reclaim")
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.active == nil {
		return errors.New("no active workload available to reclaim")
	}
	manager.active = nil
	manager.state = lease.StateIdle
	return nil
}

func (manager *fakeManager) State() lease.State {
	manager.mu.Lock()
	defer manager.mu.Unlock()
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

// shortSocketPath returns a socket path under a fresh 0700 directory. Unix
// socket paths are limited to about 104 bytes, which t.TempDir can exceed.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "cn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	return filepath.Join(directory, "agent", "control.sock")
}

// startServer runs a control server for manager and returns its socket path.
func startServer(t *testing.T, manager Manager, recorder *Recorder) string {
	t.Helper()
	path := shortSocketPath(t)
	listener, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen() returned unexpected error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- NewServer(manager, recorder, []allowlist.Entry{testImage}, nil).Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("Serve() returned unexpected error: %v", err)
		}
	})
	return path
}

func send(t *testing.T, path string, request Request) (Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return Send(ctx, path, request)
}

func TestRunStartsLocalLeaseAndCollectsResult(t *testing.T) {
	manager := newFakeManager()
	path := startServer(t, manager, NewRecorder(nopCoordinator{}))

	before := time.Now()
	response, err := send(t, path, Request{Command: CommandRun, Digest: testImage.Digest, Seconds: 600})
	if err != nil {
		t.Fatalf("run returned unexpected error: %v", err)
	}
	if !strings.HasPrefix(response.LeaseID, "local-") {
		t.Errorf("lease ID = %q, want local- prefix", response.LeaseID)
	}
	if response.State != lease.StateRunning || response.Lease == nil || response.Lease.ID != response.LeaseID {
		t.Errorf("status after run = %+v", response)
	}

	manager.mu.Lock()
	offer := manager.offers[0]
	manager.mu.Unlock()
	if offer.ImageDigest != testImage.Digest {
		t.Errorf("offered digest = %q, want %q", offer.ImageDigest, testImage.Digest)
	}
	if lifetime := offer.ExpiresAt.Sub(before); lifetime < 599*time.Second || lifetime > 601*time.Second {
		t.Errorf("offer lifetime = %v, want about 10m", lifetime)
	}
	select {
	case <-manager.waits:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not collect the lease result")
	}
}

func TestRunDefaultsAndLimitsLeaseLength(t *testing.T) {
	manager := newFakeManager()
	path := startServer(t, manager, NewRecorder(nopCoordinator{}))

	if _, err := send(t, path, Request{Command: CommandRun, Digest: testImage.Digest}); err != nil {
		t.Fatalf("run returned unexpected error: %v", err)
	}
	manager.mu.Lock()
	lifetime := time.Until(manager.offers[0].ExpiresAt)
	manager.mu.Unlock()
	if lifetime < 29*time.Minute || lifetime > 30*time.Minute {
		t.Errorf("default lifetime = %v, want 30m", lifetime)
	}

	for _, seconds := range []int{-1, int(MaxRunDuration.Seconds()) + 1} {
		if _, err := send(t, path, Request{Command: CommandRun, Digest: testImage.Digest, Seconds: seconds}); err == nil {
			t.Errorf("run with %ds returned nil error", seconds)
		}
	}
	if _, err := send(t, path, Request{Command: CommandRun}); err == nil {
		t.Error("run without a digest returned nil error")
	}
}

func TestRunReportsRejectionReason(t *testing.T) {
	manager := newFakeManager()
	manager.rejectErr = &lease.OfferRejectedError{Reason: lease.RejectionDraining}
	path := startServer(t, manager, NewRecorder(nopCoordinator{}))

	_, err := send(t, path, Request{Command: CommandRun, Digest: testImage.Digest})
	if err == nil || !strings.Contains(err.Error(), "draining") {
		t.Fatalf("run error = %v, want draining rejection", err)
	}
}

func TestDrainResumeReclaimAndStatus(t *testing.T) {
	manager := newFakeManager()
	recorder := NewRecorder(nopCoordinator{})
	path := startServer(t, manager, recorder)

	if response, err := send(t, path, Request{Command: CommandDrain}); err != nil || response.State != lease.StateDraining {
		t.Fatalf("drain = %+v, %v", response, err)
	}
	if response, err := send(t, path, Request{Command: CommandResume}); err != nil || response.State != lease.StateIdle {
		t.Fatalf("resume = %+v, %v", response, err)
	}
	if _, err := send(t, path, Request{Command: CommandReclaim}); err == nil {
		t.Fatal("reclaim with no job returned nil error")
	}

	code := 0
	if err := recorder.ReportResult(context.Background(), lease.Result{LeaseID: "local-1", Outcome: lease.OutcomeCompleted, ExitCode: &code}); err != nil {
		t.Fatal(err)
	}
	response, err := send(t, path, Request{Command: CommandStatus})
	if err != nil {
		t.Fatalf("status returned unexpected error: %v", err)
	}
	if response.LastResult == nil || response.LastResult.LeaseID != "local-1" || response.LastResult.Outcome != lease.OutcomeCompleted {
		t.Fatalf("last result = %+v", response.LastResult)
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()
	if strings.Join(manager.commands, ",") != "drain,resume,reclaim" {
		t.Errorf("commands = %v", manager.commands)
	}
}

func TestImagesAndUnknownCommand(t *testing.T) {
	path := startServer(t, newFakeManager(), NewRecorder(nopCoordinator{}))

	response, err := send(t, path, Request{Command: CommandImages})
	if err != nil || len(response.Images) != 1 || response.Images[0] != testImage {
		t.Fatalf("images = %+v, %v", response, err)
	}
	if _, err := send(t, path, Request{Command: "format-disk"}); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("unknown command error = %v", err)
	}
}

func TestSendExplainsWhenNoAgentIsRunning(t *testing.T) {
	_, err := send(t, shortSocketPath(t), Request{Command: CommandStatus})
	if err == nil || !strings.Contains(err.Error(), "node-agent -serve") {
		t.Fatalf("error = %v, want a hint to start node-agent", err)
	}
}

func TestListenRestrictsSocketAccess(t *testing.T) {
	path := shortSocketPath(t)
	listener, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen() returned unexpected error: %v", err)
	}
	defer listener.Close()

	for name, want := range map[string]os.FileMode{filepath.Dir(path): 0o700, path: 0o600} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", name, got, want)
		}
	}

	if _, err := Listen(path); err == nil || !strings.Contains(err.Error(), "already listening") {
		t.Fatalf("second Listen() error = %v, want already listening", err)
	}
}

func TestListenReplacesStaleSocket(t *testing.T) {
	path := shortSocketPath(t)
	listener, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a crashed agent: the socket file stays but nothing accepts.
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	listener.Close()

	listener, err = Listen(path)
	if err != nil {
		t.Fatalf("Listen() over stale socket returned error: %v", err)
	}
	listener.Close()
}

func TestListenRefusesUnsafeDirectory(t *testing.T) {
	path := shortSocketPath(t)
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(path); err == nil || !strings.Contains(err.Error(), "want 700") {
		t.Fatalf("Listen() in a 0755 directory error = %v", err)
	}

	link := filepath.Join(filepath.Dir(directory), "link")
	if err := os.Symlink(directory, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(filepath.Join(link, "control.sock")); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("Listen() through a symlink error = %v", err)
	}
}
