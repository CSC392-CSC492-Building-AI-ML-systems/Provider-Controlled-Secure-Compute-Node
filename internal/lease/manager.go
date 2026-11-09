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
	waiting     bool
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

	launchCtx, cancel := context.WithDeadline(ctx, offer.ExpiresAt)
	defer cancel()
	workload, err := manager.engine.Launch(launchCtx, offer.ImageDigest)
	if err != nil {
		launchErr := fmt.Errorf("launch lease workload: %w", err)
		if workload != nil {
			manager.setWorkload(workload)
		} else {
			manager.clearActiveLease()
		}

		if reportErr := manager.coordinator.ReportResult(ctx, Result{
			LeaseID: offer.ID,
			Outcome: OutcomeFailed,
		}); reportErr != nil {
			return errors.Join(
				launchErr,
				fmt.Errorf("report lease launch failure: %w", reportErr),
			)
		}
		return launchErr
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
	manager.waiting = false
	manager.state = StateIdle
}

// Wait collects the active workload's result and releases the node for another
// offer. Call after ReceiveOffer succeeds. Cancellation or expiry kills the job.
func (manager *Manager) Wait(ctx context.Context) error {
	manager.mu.Lock()
	if manager.workload == nil || manager.waiting {
		manager.mu.Unlock()
		return errors.New("no active workload available to wait for")
	}
	workload, active := manager.workload, *manager.activeLease
	manager.waiting = true
	manager.mu.Unlock()

	waitCtx, cancel := context.WithDeadline(ctx, active.ExpiresAt)
	defer cancel()
	code, waitErr := workload.Wait(waitCtx)
	result := Result{LeaseID: active.ID, Outcome: OutcomeCompleted, ExitCode: &code}
	if waitErr != nil {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		killErr := workload.Kill(cleanupCtx)
		cleanupCancel()
		if killErr != nil {
			manager.mu.Lock()
			manager.waiting = false
			manager.mu.Unlock()
			return errors.Join(waitErr, fmt.Errorf("terminate workload: %w", killErr))
		}
		result.ExitCode = nil
	}
	if waitErr != nil || code != 0 {
		result.Outcome = OutcomeFailed
	}
	if !manager.now().Before(active.ExpiresAt) {
		result.Outcome = OutcomeExpired
	}
	// Report before allowing another offer. A cancelled job context must not
	// suppress the terminal report.
	reportCtx, reportCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer reportCancel()
	reportErr := manager.coordinator.ReportResult(reportCtx, result)
	manager.clearActiveLease()
	if reportErr != nil {
		reportErr = fmt.Errorf("report lease result: %w", reportErr)
	}
	return errors.Join(waitErr, reportErr)
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
