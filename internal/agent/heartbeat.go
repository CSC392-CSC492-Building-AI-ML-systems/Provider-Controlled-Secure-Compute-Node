// Package agent is the node's control loop: registration, heartbeats, and leases.
package agent

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/coordinator"
)

// Heartbeat registers the node and keeps it alive with the coordinator.
// See docs/coordinator-requirements.md §2 and §3 (R1, R7, H1–H10).
type Heartbeat struct {
	Client       coordinator.Client
	Registration coordinator.Registration
	// Interval between heartbeats. Default 5s, a third of P16's 15s stale timeout.
	Interval time.Duration
	// LostAfter is how long without a successful heartbeat before we assume the
	// coordinator has revoked our leases. Default 15s, P16's stale timeout.
	LostAfter time.Duration
	// OnRegistered runs on the heartbeat goroutine after each successful
	// registration: once at startup, and again after re-registering. Offers
	// must not be taken before the first call (R1).
	OnRegistered func()
	// OnLost runs on the heartbeat goroutine, once per outage, when our leases
	// are probably revoked: no heartbeat for LostAfter, or the coordinator
	// forgot or removed us.
	OnLost func()
	// OnRecovered runs once after an outage, after a successful heartbeat or
	// re-registration. It does not run on initial registration.
	OnRecovered func()
	// OnStatus receives the coordinator's view after each successful heartbeat
	// and receives DRAINED when the coordinator removes the node.
	OnStatus func(coordinator.ProviderStatus)
	Log      *slog.Logger
}

// Run registers, then heartbeats until ctx is cancelled (returns nil) or the
// coordinator removes us (returns an error wrapping coordinator.ErrRemoved).
// Callers run it on its own goroutine so job work can never delay a heartbeat.
func (h *Heartbeat) Run(ctx context.Context) error {
	h.defaults()
	if err := h.registerUntilDone(ctx); err != nil {
		return err
	}
	lost := false
	markLost := func() {
		if !lost {
			lost = true
			h.OnLost()
		}
	}
	recovered := func() {
		if lost {
			lost = false
			h.OnRecovered()
		}
	}
	t := time.NewTicker(h.Interval)
	defer t.Stop()
	deadline := time.NewTimer(h.LostAfter)
	defer deadline.Stop()
	last := time.Now()
	timedOut := func() {
		h.Log.Warn("no heartbeat within lost timeout, assuming leases revoked", "lost_after", h.LostAfter)
		markLost()
	}
	needsRegistration := false
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-deadline.C:
			timedOut()
			continue
		case <-t.C:
		}

		if !needsRegistration {
			beatCtx := ctx
			cancel := func() {}
			if !lost {
				beatCtx, cancel = context.WithDeadline(ctx, last.Add(h.LostAfter))
			}
			status, err := h.Client.Heartbeat(beatCtx)
			cancel()
			if ctx.Err() != nil {
				return nil
			}
			switch {
			case errors.Is(err, coordinator.ErrRemoved):
				markLost()
				h.OnStatus(coordinator.ProviderDrained)
				h.Log.Warn("removed by coordinator, stopping heartbeats", "err", err)
				return err
			case errors.Is(err, coordinator.ErrUnknownProvider):
				// The coordinator lost its state, so our leases are gone too.
				h.Log.Warn("coordinator does not know us, re-registering", "err", err)
				markLost()
				deadline.Stop()
				needsRegistration = true
			case err != nil:
				h.Log.Warn("heartbeat failed", "err", err, "since_last_ok", time.Since(last).Round(time.Millisecond))
				if !lost && time.Since(last) >= h.LostAfter {
					deadline.Stop()
					timedOut()
				}
				continue
			default:
				if !lost && time.Since(last) >= h.LostAfter {
					timedOut()
				}
				h.OnStatus(status)
			}
		}
		if needsRegistration {
			if err := h.register(ctx); err != nil {
				h.Log.Warn("re-register failed", "err", err)
				continue
			}
			needsRegistration = false
		}

		last = time.Now()
		deadline.Reset(h.LostAfter)
		recovered()
	}
}

func (h *Heartbeat) defaults() {
	if h.Interval <= 0 {
		h.Interval = 5 * time.Second
	}
	if h.LostAfter <= 0 {
		h.LostAfter = 15 * time.Second
	}
	if h.OnRegistered == nil {
		h.OnRegistered = func() {}
	}
	if h.OnLost == nil {
		h.OnLost = func() {}
	}
	if h.OnRecovered == nil {
		h.OnRecovered = func() {}
	}
	if h.OnStatus == nil {
		h.OnStatus = func(coordinator.ProviderStatus) {}
	}
	if h.Log == nil {
		h.Log = slog.Default()
	}
}

// registerUntilDone retries transient failures (coordinator down, network) every
// Interval. A rejected request (4xx) means our registration is wrong, so it stops.
func (h *Heartbeat) registerUntilDone(ctx context.Context) error {
	for {
		err := h.register(ctx)
		if err == nil {
			return nil
		}
		var apiErr *coordinator.APIError
		if errors.As(err, &apiErr) && apiErr.Status >= 400 && apiErr.Status < 500 {
			return err
		}
		h.Log.Warn("register failed, retrying", "err", err, "in", h.Interval)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(h.Interval):
		}
	}
}

func (h *Heartbeat) register(ctx context.Context) error {
	err := h.Client.Register(ctx, h.Registration)
	if errors.Is(err, coordinator.ErrAlreadyRegistered) {
		// P16 has no update endpoint, so changed capabilities are not sent (R7).
		h.Log.Info("already registered, continuing")
		h.OnRegistered()
		return nil
	}
	if err == nil {
		h.Log.Info("registered")
		h.OnRegistered()
	}
	return err
}
