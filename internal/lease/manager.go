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

// ErrNoActiveWorkload indicates that the specified workload has already left the manager.
var ErrNoActiveWorkload = errors.New("no active workload available to wait for")

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
	launchDone  chan struct{}
	waiting     bool
	// waitDone is closed when the in-progress Wait returns.
	waitDone chan struct{}
	// finishing means Wait owns final cleanup and reporting.
	finishing bool
	// reclaimed marks a manual reclaim. killDone lets Wait observe whether
	// the removal succeeded before it reports or releases the lease.
	reclaimed bool
	killDone  chan struct{}
	// disconnected means the coordinator cannot be reached, so any lease we
	// accepted may already be revoked. Local-only nodes never set it.
	disconnected bool
	// revocation cancels launch and wait for the lease that was active at loss.
	revocation context.Context
	revoke     context.CancelFunc
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

	revocation, launchDone, rejectionReason := manager.reserveAcceptedOffer(offer)
	if rejectionReason != "" {
		return manager.rejectOffer(ctx, offer.ID, rejectionReason)
	}
	defer close(launchDone)

	if err := manager.coordinator.ReportAccepted(ctx, offer.ID); err != nil {
		manager.clearActiveLease()
		return fmt.Errorf("report lease acceptance: %w", err)
	}

	launchCtx, cancel := workloadContext(ctx, revocation, offer.ExpiresAt)
	defer cancel()
	var workload Workload
	if offer.Request != nil {
		requestEngine, supportsRequest := manager.engine.(RequestContainerEngine)
		if !supportsRequest {
			err = errors.New("container engine does not support inference requests")
		} else {
			workload, err = requestEngine.LaunchRequest(launchCtx, offer.ImageDigest, offer.Request)
		}
	} else {
		workload, err = manager.engine.Launch(launchCtx, offer.ImageDigest)
	}
	// Publish the workload under the lock so reclaim either cancels setup
	// or owns the returned workload, including when they finish together.
	manager.mu.Lock()
	revoked := revocation.Err() != nil
	if !revoked {
		manager.workload = workload
	}
	manager.mu.Unlock()
	if revoked {
		// Launch can return a handle even on error when cleanup failed.
		if workload != nil {
			if killErr := killWorkload(workload); killErr != nil {
				manager.mu.Lock()
				manager.workload = workload
				manager.reclaimed = false
				manager.mu.Unlock()
				return errors.Join(err, fmt.Errorf("clean up revoked workload: %w", killErr))
			}
		}
		return errors.Join(errors.New("lease revoked during launch"), err,
			manager.reportReclaimed(offer.ID))
	}
	if err != nil {
		launchErr := fmt.Errorf("launch lease workload: %w", err)
		if workload == nil {
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

	if manager.state == StateDraining {
		return RejectionDraining
	}
	if manager.disconnected {
		return RejectionDisconnected
	}
	if manager.activeLease != nil || manager.state == StateRunning {
		return RejectionAlreadyActive
	}
	return ""
}

func (manager *Manager) reserveAcceptedOffer(offer Offer) (context.Context, chan struct{}, RejectionReason) {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	if manager.state == StateDraining {
		return nil, nil, RejectionDraining
	}
	if manager.disconnected {
		return nil, nil, RejectionDisconnected
	}
	if manager.activeLease != nil || manager.state == StateRunning {
		return nil, nil, RejectionAlreadyActive
	}

	manager.activeLease = &Active{
		ID:          offer.ID,
		ImageDigest: offer.ImageDigest,
		ExpiresAt:   offer.ExpiresAt,
		Request:     offer.Request,
	}
	manager.state = StateRunning
	manager.revocation, manager.revoke = context.WithCancel(context.Background())
	manager.launchDone = make(chan struct{})
	return manager.revocation, manager.launchDone, ""
}

func (manager *Manager) clearActiveLease() {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	manager.revoke()
	manager.revocation, manager.revoke = nil, nil
	manager.activeLease = nil
	manager.workload = nil
	manager.launchDone = nil
	manager.waiting = false
	manager.waitDone = nil
	manager.finishing = false
	manager.reclaimed = false
	manager.killDone = nil
	if manager.state != StateDraining {
		manager.state = StateIdle
	}
}

// Wait collects the active workload's result and clears the active lease.
// Call after ReceiveOffer succeeds. Cancellation or expiry kills the job.
func (manager *Manager) Wait(ctx context.Context) error {
	return manager.wait(ctx, "")
}

// WaitForOffer waits only for the named lease. If reclaim wins the race to
// own the workload, wait for its report before returning ErrNoActiveWorkload.
func (manager *Manager) WaitForOffer(ctx context.Context, leaseID string) error {
	return manager.wait(ctx, leaseID)
}

func (manager *Manager) wait(ctx context.Context, leaseID string) error {
	manager.mu.Lock()
	for leaseID != "" && manager.activeLease != nil && manager.activeLease.ID == leaseID && manager.waitDone != nil {
		done := manager.waitDone
		manager.mu.Unlock()
		<-done
		manager.mu.Lock()
	}
	if manager.workload == nil || manager.activeLease == nil || manager.waiting || (leaseID != "" && manager.activeLease.ID != leaseID) {
		manager.mu.Unlock()
		return ErrNoActiveWorkload
	}
	workload, active, revocation := manager.workload, *manager.activeLease, manager.revocation
	manager.waiting = true
	done := make(chan struct{})
	manager.waitDone = done
	manager.mu.Unlock()
	defer close(done)

	waitCtx, cancel := workloadContext(ctx, revocation, active.ExpiresAt)
	defer cancel()
	code, waitErr := workload.Wait(waitCtx)

	manager.mu.Lock()
	manager.finishing = true
	killDone := manager.killDone
	manager.mu.Unlock()
	if killDone != nil {
		<-killDone
	}
	manager.mu.RLock()
	reclaimed := manager.reclaimed
	manager.mu.RUnlock()
	if reclaimed {
		// Reclaim already removed the workload; its Wait error is expected.
		return manager.reportReclaimed(active.ID)
	}

	result := Result{LeaseID: active.ID, Outcome: OutcomeCompleted, ExitCode: &code}
	if resultWorkload, ok := workload.(ResultWorkload); ok {
		if response, found := resultWorkload.Response(); found {
			result.Inference = &response
		}
	}
	if waitErr != nil || revocation.Err() != nil {
		if killErr := killWorkload(workload); killErr != nil {
			manager.mu.Lock()
			manager.waiting = false
			manager.waitDone = nil
			manager.finishing = false
			manager.mu.Unlock()
			return errors.Join(waitErr, fmt.Errorf("terminate workload: %w", killErr))
		}
		if revocation.Err() != nil {
			return manager.reportReclaimed(active.ID)
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

func workloadContext(ctx, revocation context.Context, expiresAt time.Time) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithDeadline(ctx, expiresAt)
	stop := context.AfterFunc(revocation, cancel)
	if revocation.Err() != nil {
		cancel()
	}
	return ctx, func() {
		stop()
		cancel()
	}
}

func killWorkload(workload Workload) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return workload.Kill(ctx)
}

// Drain stops future lease acceptance while preserving the current workload.
func (manager *Manager) Drain(_ context.Context) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	manager.state = StateDraining
	return nil
}

// Resume ends a drain so the node accepts new offers again.
func (manager *Manager) Resume(_ context.Context) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	if manager.state != StateDraining {
		return nil
	}
	if manager.activeLease != nil {
		manager.state = StateRunning
	} else {
		manager.state = StateIdle
	}
	return nil
}

// Reclaim requests immediate termination of the current workload. If Wait is
// collecting the workload, Reclaim kills it, lets that Wait report the
// reclaimed result, and returns once the node is released.
func (manager *Manager) Reclaim(ctx context.Context) error {
	manager.mu.Lock()
	if manager.activeLease == nil {
		manager.mu.Unlock()
		return errors.New("no active workload available to reclaim")
	}
	workload, active := manager.workload, manager.activeLease
	if manager.reclaimed {
		manager.mu.Unlock()
		return errors.New("workload is already being reclaimed")
	}
	if manager.finishing {
		manager.mu.Unlock()
		return errors.New("workload is already finishing")
	}
	manager.reclaimed = true
	if workload == nil {
		manager.revoke()
		done := manager.launchDone
		manager.mu.Unlock()
		return manager.reclaimLaunch(ctx, active, done)
	}
	if manager.waiting {
		done, killDone := manager.waitDone, make(chan struct{})
		manager.killDone = killDone
		manager.mu.Unlock()
		return manager.reclaimWaited(ctx, workload, done, killDone)
	}
	manager.waiting = true
	done := make(chan struct{})
	manager.waitDone = done
	manager.mu.Unlock()
	defer close(done)

	killCtx, killCancel := context.WithTimeout(ctx, 10*time.Second)
	killErr := workload.Kill(killCtx)
	killCancel()
	if killErr != nil {
		manager.mu.Lock()
		manager.waiting = false
		manager.waitDone = nil
		manager.reclaimed = false
		manager.mu.Unlock()
		return fmt.Errorf("reclaim workload: %w", killErr)
	}

	return manager.reportReclaimed(active.ID)
}

// reclaimLaunch waits for the cancel-aware launch path to clean up. Keep the
// lease reserved if the engine returned a handle that could not be removed.
func (manager *Manager) reclaimLaunch(ctx context.Context, active *Active, done <-chan struct{}) error {
	select {
	case <-done:
		manager.mu.RLock()
		defer manager.mu.RUnlock()
		if manager.activeLease == active {
			return errors.New("reclaim during launch: workload cleanup incomplete")
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("reclaim during launch: %w", ctx.Err())
	}
}

// reclaimWaited kills a workload that Wait is collecting, then waits for that
// Wait to report the reclaimed result and release the node.
func (manager *Manager) reclaimWaited(ctx context.Context, workload Workload, done <-chan struct{}, killDone chan struct{}) error {
	killCtx, killCancel := context.WithTimeout(ctx, 10*time.Second)
	killErr := workload.Kill(killCtx)
	killCancel()
	manager.mu.Lock()
	manager.reclaimed = killErr == nil
	manager.killDone = nil
	close(killDone)
	manager.mu.Unlock()
	if killErr != nil {
		return fmt.Errorf("reclaim workload: %w", killErr)
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for reclaimed workload to be released: %w", ctx.Err())
	}
}

func (manager *Manager) reportReclaimed(leaseID string) error {
	result := Result{LeaseID: leaseID, Outcome: OutcomeReclaimed}
	reportCtx, reportCancel := context.WithTimeout(context.Background(), 10*time.Second)
	reportErr := manager.coordinator.ReportResult(reportCtx, result)
	reportCancel()
	manager.clearActiveLease()
	if reportErr != nil {
		return fmt.Errorf("report reclaimed lease: %w", reportErr)
	}
	return nil
}

// SetCoordinatorConnected records coordinator connectivity changes. While
// disconnected, new offers are rejected with RejectionDisconnected and the
// active lease's launch or wait is cancelled so its owner cleans it up.
func (manager *Manager) SetCoordinatorConnected(connected bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()

	manager.disconnected = !connected
	if !connected && manager.revoke != nil {
		manager.revoke()
	}
}

// CoordinatorConnected reports whether the coordinator is currently reachable.
func (manager *Manager) CoordinatorConnected() bool {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	return !manager.disconnected
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
