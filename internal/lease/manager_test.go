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
			manager := NewManager(
				&stubAllowlist{allowed: test.allowed},
				nil,
				nil,
			)
			manager.now = func() time.Time { return fixedNow }

			err := manager.ReceiveOffer(context.Background(), test.offer)
			if test.expectedReason == "" {
				if err != nil {
					t.Fatalf("ReceiveOffer() returned unexpected error: %v", err)
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
		})
	}
}

func TestReceiveOfferReturnsAllowlistError(t *testing.T) {
	expectedError := errors.New("allowlist unavailable")
	manager := NewManager(
		&stubAllowlist{err: expectedError},
		nil,
		nil,
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
