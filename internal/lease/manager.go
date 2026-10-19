package lease

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
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
	mu          sync.RWMutex
	allowlist   ImageAllowlist
	engine      ContainerEngine
	coordinator Coordinator
	now         func() time.Time
	state       State
	activeLease *Active
	workload    Workload
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
		state:       StateIdle,
	}
}

// ReceiveOffer is the entry point for a coordinator lease offer.
func (manager *Manager) ReceiveOffer(ctx context.Context, offer Offer) error {
	if rejectionReason := manager.availabilityRejection(); rejectionReason != "" {
		return manager.rejectOffer(ctx, offer.ID, rejectionReason)
	}

	rejectionReason, err := manager.validateOffer(ctx, offer)
	if err != nil {
		return err
	}
	if rejectionReason != "" {
		return manager.rejectOffer(ctx, offer.ID, rejectionReason)
	}

	if rejectionReason := manager.reserveAcceptedOffer(offer); rejectionReason != "" {
		return manager.rejectOffer(ctx, offer.ID, rejectionReason)
	}

	if err := manager.coordinator.ReportAccepted(ctx, offer.ID); err != nil {
		manager.clearActiveLease()
		return fmt.Errorf("report lease acceptance: %w", err)
	}

	workload, err := manager.engine.Launch(ctx, offer.ImageDigest)
	if err != nil {
		return fmt.Errorf("launch lease workload: %w", err)
	}
	manager.setWorkload(workload)

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

func (manager *Manager) rejectOffer(
	ctx context.Context,
	leaseID string,
	reason RejectionReason,
) error {
	if err := manager.coordinator.ReportRejected(ctx, leaseID, reason); err != nil {
		return fmt.Errorf("report lease rejection: %w", err)
	}
	return &OfferRejectedError{Reason: reason}
}

func (manager *Manager) availabilityRejection() RejectionReason {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	if manager.activeLease != nil || manager.state == StateRunning {
		return RejectionAlreadyActive
	}
	if manager.state == StateDraining {
		return RejectionDraining
	}
	return ""
}

func (manager *Manager) reserveAcceptedOffer(offer Offer) RejectionReason {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	if manager.activeLease != nil || manager.state == StateRunning {
		return RejectionAlreadyActive
	}
	if manager.state == StateDraining {
		return RejectionDraining
	}

	manager.activeLease = &Active{
		ID:          offer.ID,
		ImageDigest: offer.ImageDigest,
		ExpiresAt:   offer.ExpiresAt,
	}
	manager.state = StateRunning
	return ""
}

func (manager *Manager) setWorkload(workload Workload) {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	manager.workload = workload
}

func (manager *Manager) clearActiveLease() {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	manager.activeLease = nil
	manager.workload = nil
	manager.state = StateIdle
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
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	return manager.state
}

// ActiveLease returns the current lease, if one exists.
func (manager *Manager) ActiveLease() (Active, bool) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	if manager.activeLease == nil {
		return Active{}, false
	}
	return *manager.activeLease, true
}
