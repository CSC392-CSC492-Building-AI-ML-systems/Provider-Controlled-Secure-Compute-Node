package coordinator

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

// fakeClient records the lease calls a LeaseReporter makes.
type fakeClient struct {
	Client // nil; calling an unexpected method panics

	err      error
	accepted []string
	rejected []rejection
}

type rejection struct {
	leaseID string
	reason  RejectReason
}

func (f *fakeClient) Accept(_ context.Context, leaseID string) (time.Time, error) {
	f.accepted = append(f.accepted, leaseID)
	return time.Time{}, f.err
}

func (f *fakeClient) Reject(_ context.Context, leaseID string, reason RejectReason) error {
	f.rejected = append(f.rejected, rejection{leaseID, reason})
	return f.err
}

func TestReportAcceptedCallsAccept(t *testing.T) {
	client := &fakeClient{}
	if err := NewLeaseReporter(client).ReportAccepted(context.Background(), "lease-1"); err != nil {
		t.Fatal(err)
	}
	if len(client.accepted) != 1 || client.accepted[0] != "lease-1" {
		t.Fatalf("accepted = %v, want [lease-1]", client.accepted)
	}
}

func TestReportAcceptedReturnsClientError(t *testing.T) {
	client := &fakeClient{err: ErrLeaseGone}
	err := NewLeaseReporter(client).ReportAccepted(context.Background(), "lease-1")
	if !errors.Is(err, ErrLeaseGone) {
		t.Fatalf("err = %v, want ErrLeaseGone", err)
	}
}

func TestReportRejectedMapsReasons(t *testing.T) {
	tests := []struct {
		reason lease.RejectionReason
		want   RejectReason
	}{
		{lease.RejectionDigestNotAllowed, RejectPolicy},
		{lease.RejectionInvalidOffer, RejectPolicy},
		{lease.RejectionAlreadyActive, RejectLocalBusy},
		{lease.RejectionDraining, RejectTempUnavailable},
		{lease.RejectionDisconnected, RejectTempUnavailable},
		{lease.RejectionExpired, RejectTempUnavailable},
		{lease.RejectionReason("something_new"), RejectTempUnavailable},
	}
	for _, tt := range tests {
		t.Run(string(tt.reason), func(t *testing.T) {
			client := &fakeClient{}
			if err := NewLeaseReporter(client).ReportRejected(context.Background(), "lease-1", tt.reason); err != nil {
				t.Fatal(err)
			}
			want := []rejection{{"lease-1", tt.want}}
			if !slices.Equal(client.rejected, want) {
				t.Fatalf("rejected = %v, want %v", client.rejected, want)
			}
		})
	}
}
