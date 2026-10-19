package lease

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotImplemented marks intentionally unfinished lease behavior.
var ErrNotImplemented = errors.New("lease behavior is not implemented")

// OfferRejectedError describes a lease offer that failed validation.
type OfferRejectedError struct {
	Reason RejectionReason
}

func (err *OfferRejectedError) Error() string {
	return fmt.Sprintf("lease offer rejected: %s", err.Reason)
}

// Manager coordinates the node-side lease lifecycle.
type Manager struct {
	allowlist   ImageAllowlist
	engine      ContainerEngine
	coordinator Coordinator
	now         func() time.Time
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
		now:         time.Now,
	}
}

// ReceiveOffer is the entry point for a coordinator lease offer.
func (manager *Manager) ReceiveOffer(ctx context.Context, offer Offer) error {
	rejectionReason, err := manager.validateOffer(ctx, offer)
	if err != nil {
		return err
	}
	if rejectionReason != "" {
		return &OfferRejectedError{Reason: rejectionReason}
	}

	return nil
}

func (manager *Manager) validateOffer(
	ctx context.Context,
	offer Offer,
) (RejectionReason, error) {
	if strings.TrimSpace(offer.ID) == "" ||
		strings.TrimSpace(offer.ImageDigest) == "" ||
		offer.ExpiresAt.IsZero() {
		return RejectionInvalidOffer, nil
	}

	if !offer.ExpiresAt.After(manager.now()) {
		return RejectionExpired, nil
	}

	allowed, err := manager.allowlist.IsAllowed(ctx, offer.ImageDigest)
	if err != nil {
		return "", fmt.Errorf("check image allowlist: %w", err)
	}
	if !allowed {
		return RejectionDigestNotAllowed, nil
	}

	return "", nil
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
