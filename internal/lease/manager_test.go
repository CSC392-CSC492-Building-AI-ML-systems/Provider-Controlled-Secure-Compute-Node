package lease

import (
	"context"
	"errors"
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

type rejectionReport struct {
	leaseID string
	reason  RejectionReason
}

type stubCoordinator struct {
	rejections   []rejectionReport
	rejectionErr error
	accepted     []string
	acceptedErr  error
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
	_ Result,
) error {
	return nil
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
			manager := NewManager(
				&stubAllowlist{allowed: test.allowed},
				nil,
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
	manager := NewManager(
		&stubAllowlist{allowed: true},
		nil,
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
