package lease

import (
	"context"
	"errors"
)

// ErrNotImplemented marks intentionally unfinished lease behavior.
var ErrNotImplemented = errors.New("lease behavior is not implemented")

// Manager coordinates the node-side lease lifecycle.
type Manager struct {
	allowlist   ImageAllowlist
	engine      ContainerEngine
	coordinator Coordinator
}

// NewManager wires the lease manager to its external dependencies.
func NewManager(
	allowlist ImageAllowlist,
	engine ContainerEngine,
	coordinator Coordinator,
) *Manager {
	return &Manager{
		allowlist:   allowlist,
		engine:      engine,
		coordinator: coordinator,
	}
}

// ReceiveOffer is the entry point for a coordinator lease offer.
func (manager *Manager) ReceiveOffer(_ context.Context, _ Offer) error {
	return ErrNotImplemented
}

// Drain stops future lease acceptance while preserving the current workload.
func (manager *Manager) Drain(_ context.Context) error {
	return ErrNotImplemented
}

// Reclaim requests immediate termination of the current workload.
func (manager *Manager) Reclaim(_ context.Context) error {
	return ErrNotImplemented
}

// SetCoordinatorConnected records coordinator connectivity changes.
func (manager *Manager) SetCoordinatorConnected(_ bool) {
	// Intentionally left as a lifecycle implementation point.
}

// State returns the manager's current lifecycle state.
func (manager *Manager) State() State {
	return StateIdle
}

// ActiveLease returns the current lease, if one exists.
func (manager *Manager) ActiveLease() (Active, bool) {
	return Active{}, false
}
