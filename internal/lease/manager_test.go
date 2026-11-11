package lease

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type stubAllowlist struct {
	allowed bool
	err     error
}

func (allowlist *stubAllowlist) IsAllowed(
	_ context.Context,
	_ string,
) (bool, error) {
	return allowlist.allowed, allowlist.err
}

type stubWorkload struct{}

func (workload *stubWorkload) Wait(_ context.Context) (int, error) {
	return 0, nil
}

func (workload *stubWorkload) Kill(_ context.Context) error {
	return nil
}

type stubContainerEngine struct {
	launchedDigests []string
	workload        Workload
	err             error
	launchDeadline  time.Time
}

func (engine *stubContainerEngine) Launch(
	ctx context.Context,
	imageDigest string,
) (Workload, error) {
	engine.launchDeadline, _ = ctx.Deadline()
	engine.launchedDigests = append(engine.launchedDigests, imageDigest)
	if engine.err != nil {
		return engine.workload, engine.err
	}
	if engine.workload == nil {
		engine.workload = &stubWorkload{}
	}
	return engine.workload, nil
}

type rejectionReport struct {
	leaseID string
	reason  RejectionReason
}

type stubCoordinator struct {
	rejections   []rejectionReport
	rejectionErr error
	accepted     []string
	acceptedErr  error
	results      []Result
	resultErr    error
}

func (coordinator *stubCoordinator) ReportAccepted(
	_ context.Context,
	leaseID string,
) error {
	coordinator.accepted = append(coordinator.accepted, leaseID)
	return coordinator.acceptedErr
}

func (coordinator *stubCoordinator) ReportRejected(
	_ context.Context,
	leaseID string,
	reason RejectionReason,
) error {
	coordinator.rejections = append(coordinator.rejections, rejectionReport{
		leaseID: leaseID,
		reason:  reason,
	})
	return coordinator.rejectionErr
}

func (coordinator *stubCoordinator) ReportResult(
	_ context.Context,
	result Result,
) error {
	coordinator.results = append(coordinator.results, result)
	return coordinator.resultErr
}

func TestReceiveOfferValidatesOffer(t *testing.T) {
	fixedNow := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name           string
		offer          Offer
		allowed        bool
		expectedReason RejectionReason
	}{
		{
			name: "missing lease ID",
			offer: Offer{
				ImageDigest: "sha256:approved",
				ExpiresAt:   fixedNow.Add(time.Minute),
			},
			allowed:        true,
			expectedReason: RejectionInvalidOffer,
		},
		{
			name: "missing image digest",
			offer: Offer{
				ID:        "lease-1",
				ExpiresAt: fixedNow.Add(time.Minute),
			},
			allowed:        true,
			expectedReason: RejectionInvalidOffer,
		},
		{
			name: "missing expiration",
			offer: Offer{
				ID:          "lease-1",
				ImageDigest: "sha256:approved",
			},
			allowed:        true,
			expectedReason: RejectionInvalidOffer,
		},
		{
			name: "expired lease",
			offer: Offer{
				ID:          "lease-1",
				ImageDigest: "sha256:approved",
				ExpiresAt:   fixedNow,
			},
			allowed:        true,
			expectedReason: RejectionExpired,
		},
		{
			name: "digest not allowed",
			offer: Offer{
				ID:          "lease-1",
				ImageDigest: "sha256:unknown",
				ExpiresAt:   fixedNow.Add(time.Minute),
			},
			allowed:        false,
			expectedReason: RejectionDigestNotAllowed,
		},
		{
			name: "valid offer",
			offer: Offer{
				ID:          "lease-1",
				ImageDigest: "sha256:approved",
				ExpiresAt:   fixedNow.Add(time.Minute),
			},
			allowed: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			coordinator := &stubCoordinator{}
			engine := &stubContainerEngine{}
			manager := NewManager(
				&stubAllowlist{allowed: test.allowed},
				engine,
				coordinator,
			)
			manager.now = func() time.Time { return fixedNow }

			err := manager.ReceiveOffer(context.Background(), test.offer)
			if test.expectedReason == "" {
				if err != nil {
					t.Fatalf("ReceiveOffer() returned unexpected error: %v", err)
				}
				if len(coordinator.rejections) != 0 {
					t.Fatalf(
						"ReportRejected() calls = %d, want 0",
						len(coordinator.rejections),
					)
				}
				if len(engine.launchedDigests) != 1 {
					t.Fatalf(
						"Launch() calls = %d, want 1",
						len(engine.launchedDigests),
					)
				}
				return
			}

			var rejection *OfferRejectedError
			if !errors.As(err, &rejection) {
				t.Fatalf("ReceiveOffer() error = %v, want OfferRejectedError", err)
			}
			if rejection.Reason != test.expectedReason {
				t.Errorf(
					"ReceiveOffer() rejection reason = %q, want %q",
					rejection.Reason,
					test.expectedReason,
				)
			}
			if len(coordinator.rejections) != 1 {
				t.Fatalf(
					"ReportRejected() calls = %d, want 1",
					len(coordinator.rejections),
				)
			}
			if coordinator.rejections[0].leaseID != test.offer.ID {
				t.Errorf(
					"reported lease ID = %q, want %q",
					coordinator.rejections[0].leaseID,
					test.offer.ID,
				)
			}
			if coordinator.rejections[0].reason != test.expectedReason {
				t.Errorf(
					"reported reason = %q, want %q",
					coordinator.rejections[0].reason,
					test.expectedReason,
				)
			}
			if len(engine.launchedDigests) != 0 {
				t.Fatalf(
					"Launch() calls = %d, want 0",
					len(engine.launchedDigests),
				)
			}
		})
	}
}

func TestReceiveOfferReturnsAllowlistError(t *testing.T) {
	expectedError := errors.New("allowlist unavailable")
	manager := NewManager(
		&stubAllowlist{err: expectedError},
		nil,
		&stubCoordinator{},
	)
	manager.now = func() time.Time {
		return time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)
	}

	err := manager.ReceiveOffer(context.Background(), Offer{
		ID:          "lease-1",
		ImageDigest: "sha256:approved",
		ExpiresAt:   manager.now().Add(time.Minute),
	})

	if !errors.Is(err, expectedError) {
		t.Fatalf("ReceiveOffer() error = %v, want wrapped allowlist error", err)
	}
}

func TestReceiveOfferReturnsRejectionReportingError(t *testing.T) {
	expectedError := errors.New("coordinator unavailable")
	coordinator := &stubCoordinator{rejectionErr: expectedError}
	manager := NewManager(
		&stubAllowlist{allowed: false},
		nil,
		coordinator,
	)
	manager.now = func() time.Time {
		return time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)
	}

	err := manager.ReceiveOffer(context.Background(), Offer{
		ID:          "lease-1",
		ImageDigest: "sha256:unknown",
		ExpiresAt:   manager.now().Add(time.Minute),
	})

	if !errors.Is(err, expectedError) {
		t.Fatalf("ReceiveOffer() error = %v, want coordinator error", err)
	}
}

func TestReceiveOfferRecordsAcceptedLease(t *testing.T) {
	fixedNow := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)
	coordinator := &stubCoordinator{}
	engine := &stubContainerEngine{}
	manager := NewManager(
		&stubAllowlist{allowed: true},
		engine,
		coordinator,
	)
	manager.now = func() time.Time { return fixedNow }
	offer := Offer{
		ID:          "lease-1",
		ImageDigest: "sha256:approved",
		ExpiresAt:   fixedNow.Add(time.Minute),
	}

	if err := manager.ReceiveOffer(context.Background(), offer); err != nil {
		t.Fatalf("ReceiveOffer() returned unexpected error: %v", err)
	}

	if manager.State() != StateRunning {
		t.Errorf("State() = %q, want %q", manager.State(), StateRunning)
	}
	if !engine.launchDeadline.Equal(offer.ExpiresAt) {
		t.Fatalf("launch deadline = %v, want %v", engine.launchDeadline, offer.ExpiresAt)
	}
	activeLease, exists := manager.ActiveLease()
	if !exists {
		t.Fatal("ActiveLease() exists = false, want true")
	}
	if activeLease.ID != offer.ID {
		t.Errorf("active lease ID = %q, want %q", activeLease.ID, offer.ID)
	}
	if activeLease.ImageDigest != offer.ImageDigest {
		t.Errorf(
			"active image digest = %q, want %q",
			activeLease.ImageDigest,
			offer.ImageDigest,
		)
	}
	if !activeLease.ExpiresAt.Equal(offer.ExpiresAt) {
		t.Errorf(
			"active expiration = %v, want %v",
			activeLease.ExpiresAt,
			offer.ExpiresAt,
		)
	}
	if len(coordinator.accepted) != 1 ||
		coordinator.accepted[0] != offer.ID {
		t.Errorf(
			"accepted lease reports = %v, want [%q]",
			coordinator.accepted,
			offer.ID,
		)
	}
	if len(engine.launchedDigests) != 1 ||
		engine.launchedDigests[0] != offer.ImageDigest {
		t.Errorf(
			"launched image digests = %v, want [%q]",
			engine.launchedDigests,
			offer.ImageDigest,
		)
	}
}

func TestReceiveOfferClearsLeaseWhenAcceptanceReportFails(t *testing.T) {
	expectedError := errors.New("coordinator unavailable")
	fixedNow := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)
	manager := NewManager(
		&stubAllowlist{allowed: true},
		nil,
		&stubCoordinator{acceptedErr: expectedError},
	)
	manager.now = func() time.Time { return fixedNow }

	err := manager.ReceiveOffer(context.Background(), Offer{
		ID:          "lease-1",
		ImageDigest: "sha256:approved",
		ExpiresAt:   fixedNow.Add(time.Minute),
	})

	if !errors.Is(err, expectedError) {
		t.Fatalf("ReceiveOffer() error = %v, want coordinator error", err)
	}
	if manager.State() != StateIdle {
		t.Errorf("State() = %q, want %q", manager.State(), StateIdle)
	}
	if _, exists := manager.ActiveLease(); exists {
		t.Fatal("ActiveLease() exists = true, want false")
	}
}

func TestReceiveOfferRejectsSecondActiveLease(t *testing.T) {
	fixedNow := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)
	coordinator := &stubCoordinator{}
	engine := &stubContainerEngine{}
	manager := NewManager(
		&stubAllowlist{allowed: true},
		engine,
		coordinator,
	)
	manager.now = func() time.Time { return fixedNow }

	firstOffer := Offer{
		ID:          "lease-1",
		ImageDigest: "sha256:first",
		ExpiresAt:   fixedNow.Add(time.Minute),
	}
	if err := manager.ReceiveOffer(context.Background(), firstOffer); err != nil {
		t.Fatalf("first ReceiveOffer() returned unexpected error: %v", err)
	}

	secondOffer := Offer{
		ID:          "lease-2",
		ImageDigest: "sha256:second",
		ExpiresAt:   fixedNow.Add(time.Minute),
	}
	err := manager.ReceiveOffer(context.Background(), secondOffer)

	var rejection *OfferRejectedError
	if !errors.As(err, &rejection) {
		t.Fatalf("second ReceiveOffer() error = %v, want OfferRejectedError", err)
	}
	if rejection.Reason != RejectionAlreadyActive {
		t.Errorf(
			"second rejection reason = %q, want %q",
			rejection.Reason,
			RejectionAlreadyActive,
		)
	}
	if len(engine.launchedDigests) != 1 {
		t.Errorf("Launch() calls = %d, want 1", len(engine.launchedDigests))
	}
	activeLease, exists := manager.ActiveLease()
	if !exists || activeLease.ID != firstOffer.ID {
		t.Errorf(
			"ActiveLease() = (%+v, %t), want first lease",
			activeLease,
			exists,
		)
	}
}

func TestDrainRejectsNewOffers(t *testing.T) {
	fixedNow := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)
	coordinator := &stubCoordinator{}
	engine := &stubContainerEngine{}
	manager := NewManager(&stubAllowlist{allowed: true}, engine, coordinator)
	manager.now = func() time.Time { return fixedNow }

	if err := manager.Drain(context.Background()); err != nil {
		t.Fatalf("Drain() returned unexpected error: %v", err)
	}
	if manager.State() != StateDraining {
		t.Fatalf("State() = %q, want %q", manager.State(), StateDraining)
	}

	err := manager.ReceiveOffer(context.Background(), Offer{
		ID:          "lease-1",
		ImageDigest: "sha256:approved",
		ExpiresAt:   fixedNow.Add(time.Minute),
	})
	var rejection *OfferRejectedError
	if !errors.As(err, &rejection) {
		t.Fatalf("ReceiveOffer() error = %v, want OfferRejectedError", err)
	}
	if rejection.Reason != RejectionDraining {
		t.Errorf("rejection reason = %q, want %q", rejection.Reason, RejectionDraining)
	}
	if len(engine.launchedDigests) != 0 {
		t.Fatalf("Launch() calls = %d, want 0", len(engine.launchedDigests))
	}
	if len(coordinator.rejections) != 1 || coordinator.rejections[0].reason != RejectionDraining {
		t.Fatalf("rejections = %v, want one draining rejection", coordinator.rejections)
	}
}

func TestDrainPreservesActiveWorkAndReturnsIdleAfterWait(t *testing.T) {
	fixedNow := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)
	workload := &controlledWorkload{}
	coordinator := &stubCoordinator{}
	manager := NewManager(
		&stubAllowlist{allowed: true},
		&stubContainerEngine{workload: workload},
		coordinator,
	)
	manager.now = func() time.Time { return fixedNow }
	offer := Offer{
		ID:          "lease-1",
		ImageDigest: "sha256:approved",
		ExpiresAt:   fixedNow.Add(time.Minute),
	}

	if err := manager.ReceiveOffer(context.Background(), offer); err != nil {
		t.Fatalf("ReceiveOffer() returned unexpected error: %v", err)
	}
	if err := manager.Drain(context.Background()); err != nil {
		t.Fatalf("Drain() returned unexpected error: %v", err)
	}
	if manager.State() != StateDraining {
		t.Fatalf("State() = %q, want %q", manager.State(), StateDraining)
	}
	if _, exists := manager.ActiveLease(); !exists {
		t.Fatal("Drain() cleared the active lease")
	}

	newOfferErr := manager.ReceiveOffer(context.Background(), Offer{
		ID:          "lease-2",
		ImageDigest: "sha256:approved",
		ExpiresAt:   fixedNow.Add(time.Minute),
	})
	var rejection *OfferRejectedError
	if !errors.As(newOfferErr, &rejection) || rejection.Reason != RejectionDraining {
		t.Fatalf("new offer error = %v, want draining rejection", newOfferErr)
	}

	if err := manager.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() returned unexpected error: %v", err)
	}
	if manager.State() != StateIdle {
		t.Fatalf("State() after Wait() = %q, want %q", manager.State(), StateIdle)
	}
	if _, exists := manager.ActiveLease(); exists {
		t.Fatal("active lease remains after Wait()")
	}
	if len(coordinator.results) != 1 || coordinator.results[0].Outcome != OutcomeCompleted {
		t.Fatalf("results = %v, want one completed result", coordinator.results)
	}
}

func TestReclaimKillsWorkReportsResultAndReleasesNode(t *testing.T) {
	fixedNow := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)
	workload := &controlledWorkload{}
	coordinator := &stubCoordinator{}
	manager := NewManager(
		&stubAllowlist{allowed: true},
		&stubContainerEngine{workload: workload},
		coordinator,
	)
	manager.now = func() time.Time { return fixedNow }
	offer := Offer{
		ID:          "lease-1",
		ImageDigest: "sha256:approved",
		ExpiresAt:   fixedNow.Add(time.Minute),
	}

	if err := manager.ReceiveOffer(context.Background(), offer); err != nil {
		t.Fatalf("ReceiveOffer() returned unexpected error: %v", err)
	}
	if err := manager.Reclaim(context.Background()); err != nil {
		t.Fatalf("Reclaim() returned unexpected error: %v", err)
	}
	if !workload.killed {
		t.Fatal("Reclaim() did not kill the workload")
	}
	if manager.State() != StateIdle {
		t.Fatalf("State() after Reclaim() = %q, want %q", manager.State(), StateIdle)
	}
	if _, exists := manager.ActiveLease(); exists {
		t.Fatal("active lease remains after Reclaim()")
	}
	if len(coordinator.results) != 1 {
		t.Fatalf("ReportResult() calls = %d, want 1", len(coordinator.results))
	}
	result := coordinator.results[0]
	if result.LeaseID != offer.ID {
		t.Errorf("reported lease ID = %q, want %q", result.LeaseID, offer.ID)
	}
	if result.Outcome != OutcomeReclaimed {
		t.Errorf("reported outcome = %q, want %q", result.Outcome, OutcomeReclaimed)
	}
	if result.ExitCode != nil {
		t.Errorf("reported exit code = %v, want nil", result.ExitCode)
	}
}

func TestReclaimKeepsNodeReservedWhenKillFails(t *testing.T) {
	workload := &controlledWorkload{killErr: errors.New("container removal failed")}
	manager := NewManager(
		&stubAllowlist{allowed: true},
		&stubContainerEngine{workload: workload},
		&stubCoordinator{},
	)
	offer := Offer{
		ID:          "lease-1",
		ImageDigest: "sha256:approved",
		ExpiresAt:   time.Now().Add(time.Minute),
	}
	if err := manager.ReceiveOffer(context.Background(), offer); err != nil {
		t.Fatalf("ReceiveOffer() returned unexpected error: %v", err)
	}

	if err := manager.Reclaim(context.Background()); err == nil {
		t.Fatal("Reclaim() returned nil, want cleanup error")
	}
	if manager.State() != StateRunning {
		t.Fatalf("State() after failed Reclaim() = %q, want %q", manager.State(), StateRunning)
	}
	if _, exists := manager.ActiveLease(); !exists {
		t.Fatal("active lease was cleared after failed Reclaim()")
	}
}

type synchronizedAllowlist struct {
	arrivals sync.WaitGroup
	release  chan struct{}
}

func newSynchronizedAllowlist(expectedCalls int) *synchronizedAllowlist {
	allowlist := &synchronizedAllowlist{release: make(chan struct{})}
	allowlist.arrivals.Add(expectedCalls)
	return allowlist
}

func (allowlist *synchronizedAllowlist) IsAllowed(
	_ context.Context,
	_ string,
) (bool, error) {
	allowlist.arrivals.Done()
	<-allowlist.release
	return true, nil
}

func TestReceiveOfferAtomicallyReservesSingleLease(t *testing.T) {
	fixedNow := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)
	allowlist := newSynchronizedAllowlist(2)
	manager := NewManager(
		allowlist,
		&stubContainerEngine{},
		&stubCoordinator{},
	)
	manager.now = func() time.Time { return fixedNow }

	errorsByOffer := make(chan error, 2)
	for _, leaseID := range []string{"lease-1", "lease-2"} {
		go func(id string) {
			errorsByOffer <- manager.ReceiveOffer(context.Background(), Offer{
				ID:          id,
				ImageDigest: "sha256:approved",
				ExpiresAt:   fixedNow.Add(time.Minute),
			})
		}(leaseID)
	}

	allowlist.arrivals.Wait()
	close(allowlist.release)

	var acceptedCount int
	var alreadyActiveCount int
	for range 2 {
		err := <-errorsByOffer
		if err == nil {
			acceptedCount++
			continue
		}

		var rejection *OfferRejectedError
		if errors.As(err, &rejection) &&
			rejection.Reason == RejectionAlreadyActive {
			alreadyActiveCount++
			continue
		}
		t.Fatalf("ReceiveOffer() returned unexpected error: %v", err)
	}

	if acceptedCount != 1 {
		t.Errorf("accepted offers = %d, want 1", acceptedCount)
	}
	if alreadyActiveCount != 1 {
		t.Errorf(
			"already-active rejections = %d, want 1",
			alreadyActiveCount,
		)
	}
}

func TestReceiveOfferRecoversFromLaunchFailure(t *testing.T) {
	launchError := errors.New("runtime unavailable")
	fixedNow := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)
	coordinator := &stubCoordinator{}
	manager := NewManager(
		&stubAllowlist{allowed: true},
		&stubContainerEngine{err: launchError},
		coordinator,
	)
	manager.now = func() time.Time { return fixedNow }

	err := manager.ReceiveOffer(context.Background(), Offer{
		ID:          "lease-1",
		ImageDigest: "sha256:approved",
		ExpiresAt:   fixedNow.Add(time.Minute),
	})

	if !errors.Is(err, launchError) {
		t.Fatalf("ReceiveOffer() error = %v, want launch error", err)
	}
	if manager.State() != StateIdle {
		t.Errorf("State() = %q, want %q", manager.State(), StateIdle)
	}
	if _, exists := manager.ActiveLease(); exists {
		t.Fatal("ActiveLease() exists = true, want false")
	}
	if len(coordinator.results) != 1 {
		t.Fatalf(
			"ReportResult() calls = %d, want 1",
			len(coordinator.results),
		)
	}
	result := coordinator.results[0]
	if result.LeaseID != "lease-1" {
		t.Errorf("reported lease ID = %q, want %q", result.LeaseID, "lease-1")
	}
	if result.Outcome != OutcomeFailed {
		t.Errorf(
			"reported outcome = %q, want %q",
			result.Outcome,
			OutcomeFailed,
		)
	}
	if result.ExitCode != nil {
		t.Errorf("reported exit code = %v, want nil", result.ExitCode)
	}
}

func TestReceiveOfferPreservesLaunchAndReportingErrors(t *testing.T) {
	launchError := errors.New("runtime unavailable")
	reportError := errors.New("coordinator unavailable")
	fixedNow := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)
	manager := NewManager(
		&stubAllowlist{allowed: true},
		&stubContainerEngine{err: launchError},
		&stubCoordinator{resultErr: reportError},
	)
	manager.now = func() time.Time { return fixedNow }

	err := manager.ReceiveOffer(context.Background(), Offer{
		ID:          "lease-1",
		ImageDigest: "sha256:approved",
		ExpiresAt:   fixedNow.Add(time.Minute),
	})

	if !errors.Is(err, launchError) {
		t.Errorf("ReceiveOffer() error = %v, want launch error", err)
	}
	if !errors.Is(err, reportError) {
		t.Errorf("ReceiveOffer() error = %v, want reporting error", err)
	}
}

type controlledWorkload struct {
	code    int
	waitErr error
	killErr error
	killed  bool
}

func (workload *controlledWorkload) Wait(context.Context) (int, error) {
	return workload.code, workload.waitErr
}
func (workload *controlledWorkload) Kill(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	workload.killed = true
	return workload.killErr
}

func TestWaitReportsResultsAndReleasesNode(t *testing.T) {
	for _, scenario := range []string{"completed", "failed", "cancelled", "expired", "cleanup-failed", "report-failed"} {
		t.Run(scenario, func(t *testing.T) {
			workload := &controlledWorkload{}
			coordinator := &stubCoordinator{}
			manager := NewManager(&stubAllowlist{allowed: true}, &stubContainerEngine{workload: workload}, coordinator)
			offer := Offer{ID: "lease-1", ImageDigest: "sha256:approved", ExpiresAt: time.Now().Add(time.Minute)}
			if err := manager.ReceiveOffer(context.Background(), offer); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "failed":
				workload.code = 23
			case "cancelled", "expired", "cleanup-failed":
				workload.waitErr = context.Canceled
			case "report-failed":
				coordinator.resultErr = errors.New("report failed")
			}
			if scenario == "expired" {
				manager.now = func() time.Time { return offer.ExpiresAt }
			}
			if scenario == "cleanup-failed" {
				workload.killErr = errors.New("cleanup failed")
			}
			err := manager.Wait(context.Background())
			wantErr := scenario == "cancelled" || scenario == "expired" || scenario == "cleanup-failed" || scenario == "report-failed"
			if (err != nil) != wantErr {
				t.Fatalf("Wait error=%v", err)
			}
			if workload.waitErr != nil && !workload.killed {
				t.Fatal("wait failure did not kill workload")
			}
			if scenario == "cleanup-failed" {
				if manager.State() != StateRunning || len(coordinator.results) != 0 {
					t.Fatal("released node before container was removed")
				}
				return
			}
			if manager.State() != StateIdle {
				t.Fatal("node remains busy after workload cleanup")
			}
			if len(coordinator.results) != 1 {
				t.Fatalf("results=%v", coordinator.results)
			}
			result := coordinator.results[0]
			outcome := OutcomeCompleted
			if scenario == "failed" || scenario == "cancelled" {
				outcome = OutcomeFailed
			}
			if scenario == "expired" {
				outcome = OutcomeExpired
			}
			if result.Outcome != outcome {
				t.Fatalf("result=%v", result)
			}
			if scenario == "failed" && (result.ExitCode == nil || *result.ExitCode != 23) {
				t.Fatalf("lost job exit code: %v", result)
			}
		})
	}
}

func TestLaunchCleanupFailureKeepsNodeReserved(t *testing.T) {
	workload := &controlledWorkload{}
	launchErr := errors.New("start and cleanup failed")
	manager := NewManager(&stubAllowlist{allowed: true}, &stubContainerEngine{workload: workload, err: launchErr}, &stubCoordinator{})
	offer := Offer{ID: "lease-1", ImageDigest: "sha256:approved", ExpiresAt: time.Now().Add(time.Minute)}
	if err := manager.ReceiveOffer(context.Background(), offer); !errors.Is(err, launchErr) {
		t.Fatalf("error=%v", err)
	}
	if manager.State() != StateRunning {
		t.Fatal("released node after cleanup failure")
	}
	if _, exists := manager.ActiveLease(); !exists {
		t.Fatal("lost lease after cleanup failure")
	}
	if err := manager.ReceiveOffer(context.Background(), offer); err == nil {
		t.Fatal("accepted another job before cleanup")
	}
	// The retained handle lets the owner retry supervision/cleanup.
	if err := manager.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if manager.State() != StateIdle {
		t.Fatal("node remains reserved after successful retry")
	}
}
