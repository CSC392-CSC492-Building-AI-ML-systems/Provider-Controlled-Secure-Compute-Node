package coordinator

import (
	"context"
)

// LeaseReporter lets the lease manager report its decisions to a coordinator.
// The lease package speaks in node terms (lease.RejectionReason, lease.Outcome);
// LeaseReporter translates them into the coordinator's vocabulary so neither
// side has to know about the other.
type LeaseReporter struct {
	Client Client
}

// NewLeaseReporter wraps a coordinator client for use by the lease manager.
func NewLeaseReporter(client Client) *LeaseReporter {
	return &LeaseReporter{Client: client}
}

// ReportAccepted tells the coordinator this node took the lease. The expiry
// the coordinator returns is not used yet: the lease manager keeps the
// expiry from the offer.
func (r *LeaseReporter) ReportAccepted(ctx context.Context, leaseID string) error {
	_, err := r.Client.Accept(ctx, leaseID)
	return err
}
