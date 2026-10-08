package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/coordinator"
)

// fakeCoordinator returns scripted errors for Register and Heartbeat, one per call;
// once a script runs out, calls succeed.
type fakeCoordinator struct {
	coordinator.Client // lease methods are unused here; calling one panics
	mu                 sync.Mutex
	registerErrs       []error
	heartbeatErrs      []error
	registers          int
	heartbeats         int
}

func (f *fakeCoordinator) Register(context.Context, coordinator.Registration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registers++
	return pop(&f.registerErrs)
}

func (f *fakeCoordinator) Heartbeat(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.heartbeats++
	return pop(&f.heartbeatErrs)
}

func pop(errs *[]error) error {
	if len(*errs) == 0 {
		return nil
	}
	err := (*errs)[0]
	*errs = (*errs)[1:]
	return err
}

func runHeartbeat(t *testing.T, f *fakeCoordinator, d time.Duration) (err error, lost int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	h := &Heartbeat{
		Client:    f,
		Interval:  5 * time.Millisecond,
		LostAfter: 30 * time.Millisecond,
		OnLost:    func() { lost++ },
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return h.Run(ctx), lost
}

var (
	netErr     = errors.New("connection refused")
	badRequest = &coordinator.APIError{Status: 422, Code: "VALIDATION_ERROR"}
)

func TestHeartbeat(t *testing.T) {
	t.Run("already registered counts as registered", func(t *testing.T) {
		f := &fakeCoordinator{registerErrs: []error{coordinator.ErrAlreadyRegistered}}
		err, lost := runHeartbeat(t, f, 50*time.Millisecond)
		if err != nil || lost != 0 || f.registers != 1 || f.heartbeats == 0 {
			t.Fatalf("err=%v lost=%d registers=%d heartbeats=%d", err, lost, f.registers, f.heartbeats)
		}
	})

	t.Run("register retries while coordinator is down", func(t *testing.T) {
		f := &fakeCoordinator{registerErrs: []error{netErr, netErr}}
		err, _ := runHeartbeat(t, f, 50*time.Millisecond)
		if err != nil || f.registers != 3 || f.heartbeats == 0 {
			t.Fatalf("err=%v registers=%d heartbeats=%d", err, f.registers, f.heartbeats)
		}
	})

	t.Run("register gives up on a rejected request", func(t *testing.T) {
		f := &fakeCoordinator{registerErrs: []error{badRequest}}
		err, _ := runHeartbeat(t, f, 50*time.Millisecond)
		if !errors.Is(err, badRequest) || f.heartbeats != 0 {
			t.Fatalf("err=%v heartbeats=%d", err, f.heartbeats)
		}
	})

	t.Run("forgotten by coordinator re-registers and drops leases", func(t *testing.T) {
		f := &fakeCoordinator{heartbeatErrs: []error{coordinator.ErrUnknownProvider}}
		err, lost := runHeartbeat(t, f, 50*time.Millisecond)
		if err != nil || lost != 1 || f.registers != 2 {
			t.Fatalf("err=%v lost=%d registers=%d", err, lost, f.registers)
		}
	})

	t.Run("outage past LostAfter drops leases once, then recovers", func(t *testing.T) {
		f := &fakeCoordinator{heartbeatErrs: make([]error, 15)}
		for i := range f.heartbeatErrs {
			f.heartbeatErrs[i] = netErr
		}
		err, lost := runHeartbeat(t, f, 150*time.Millisecond)
		if err != nil || lost != 1 || f.heartbeats <= 15 {
			t.Fatalf("err=%v lost=%d heartbeats=%d", err, lost, f.heartbeats)
		}
	})

	t.Run("short blip does not drop leases", func(t *testing.T) {
		f := &fakeCoordinator{heartbeatErrs: []error{netErr}}
		_, lost := runHeartbeat(t, f, 50*time.Millisecond)
		if lost != 0 {
			t.Fatalf("lost=%d", lost)
		}
	})

	t.Run("removed by coordinator stops", func(t *testing.T) {
		f := &fakeCoordinator{heartbeatErrs: []error{coordinator.ErrRemoved}}
		err, _ := runHeartbeat(t, f, time.Second)
		if !errors.Is(err, coordinator.ErrRemoved) || f.heartbeats != 1 {
			t.Fatalf("err=%v heartbeats=%d", err, f.heartbeats)
		}
	})
}
