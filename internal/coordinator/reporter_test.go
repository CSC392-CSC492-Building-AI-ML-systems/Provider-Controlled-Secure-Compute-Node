package coordinator

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeClient records the lease calls a LeaseReporter makes.
type fakeClient struct {
	Client // nil; calling an unexpected method panics

	err      error
	accepted []string
}

func (f *fakeClient) Accept(_ context.Context, leaseID string) (time.Time, error) {
	f.accepted = append(f.accepted, leaseID)
	return time.Time{}, f.err
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
