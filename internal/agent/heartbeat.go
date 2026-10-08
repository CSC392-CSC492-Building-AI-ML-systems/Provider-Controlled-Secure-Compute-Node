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
	// OnLost runs on the heartbeat goroutine, once per outage, when our leases
	// are probably revoked: no heartbeat for LostAfter, or the coordinator forgot us.
	OnLost func()
	Log    *slog.Logger
}

// Run registers, then heartbeats until ctx is cancelled (returns nil) or the
// coordinator removes us (returns an error wrapping coordinator.ErrRemoved).
// Callers run it on its own goroutine so job work can never delay a heartbeat.
func (h *Heartbeat) Run(ctx context.Context) error {
	h.defaults()
	if err := h.registerUntilDone(ctx); err != nil {
		return err
	}
	last, lost := time.Now(), false
	t := time.NewTicker(h.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		err := h.Client.Heartbeat(ctx)
		switch {
		case err == nil:
			last, lost = time.Now(), false
		case ctx.Err() != nil:
			return nil
		case errors.Is(err, coordinator.ErrRemoved):
			h.Log.Warn("removed by coordinator, stopping heartbeats", "err", err)
			return err
		case errors.Is(err, coordinator.ErrUnknownProvider):
			// The coordinator lost its state (P16 keeps it in memory), so our leases are gone too.
			h.Log.Warn("coordinator does not know us, re-registering", "err", err)
			if !lost {
				lost = true
				h.OnLost()
			}
			if err := h.register(ctx); err != nil {
				h.Log.Warn("re-register failed", "err", err)
			} else {
				last, lost = time.Now(), false
			}
		default:
			h.Log.Warn("heartbeat failed", "err", err, "since_last_ok", time.Since(last).Round(time.Millisecond))
		}
		if !lost && time.Since(last) > h.LostAfter {
			lost = true
			h.Log.Warn("no heartbeat within lost timeout, assuming leases revoked", "lost_after", h.LostAfter)
			h.OnLost()
		}
	}
}

func (h *Heartbeat) defaults() {
	if h.Interval <= 0 {
		h.Interval = 5 * time.Second
	}
	if h.LostAfter <= 0 {
		h.LostAfter = 15 * time.Second
	}
	if h.OnLost == nil {
		h.OnLost = func() {}
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
		return nil
	}
	if err == nil {
		h.Log.Info("registered")
	}
	return err
}
