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
	reported map[string]Result
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

func (f *fakeClient) Report(_ context.Context, leaseID string, result Result) error {
	if f.reported == nil {
		f.reported = map[string]Result{}
	}
	f.reported[leaseID] = result
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

func TestReportResultMapsOutcomes(t *testing.T) {
	zero, one := 0, 1
	tests := []struct {
		name   string
		result lease.Result
		want   Result
	}{
		{"completed", lease.Result{Outcome: lease.OutcomeCompleted, ExitCode: &zero}, Result{Success: true}},
		{"job exited non-zero", lease.Result{Outcome: lease.OutcomeFailed, ExitCode: &one}, Result{Reason: FailExecution}},
		{"launch failed", lease.Result{Outcome: lease.OutcomeFailed}, Result{Reason: FailRuntime}},
		{"expired", lease.Result{Outcome: lease.OutcomeExpired}, Result{Reason: FailRuntime}},
		{"reclaimed", lease.Result{Outcome: lease.OutcomeReclaimed}, Result{Reason: FailRuntime}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeClient{}
			tt.result.LeaseID = "lease-1"
			if err := NewLeaseReporter(client).ReportResult(context.Background(), tt.result); err != nil {
				t.Fatal(err)
			}
			if got, ok := client.reported["lease-1"]; !ok || got != tt.want {
				t.Fatalf("reported = %+v, want %+v", client.reported, tt.want)
			}
		})
	}
}

func TestReportResultRefusesRejectedOutcome(t *testing.T) {
	client := &fakeClient{}
	err := NewLeaseReporter(client).ReportResult(context.Background(),
		lease.Result{LeaseID: "lease-1", Outcome: lease.OutcomeRejected})
	if err == nil {
		t.Fatal("expected an error for a rejected outcome")
	}
	if len(client.reported) != 0 {
		t.Fatalf("reported = %v, want nothing sent", client.reported)
	}
}
