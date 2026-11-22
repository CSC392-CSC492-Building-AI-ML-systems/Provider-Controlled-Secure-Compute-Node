package coordinator

import (
	"context"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
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

// ReportRejected tells the coordinator this node declined the lease.
func (r *LeaseReporter) ReportRejected(ctx context.Context, leaseID string, reason lease.RejectionReason) error {
	return r.Client.Reject(ctx, leaseID, rejectReason(reason))
}

// rejectReason maps a node rejection onto the coordinator's reasons
// (docs/coordinator-requirements.md L4). Any rejection stops the coordinator
// offering that job to us again, so reasons that may clear up on their own use
// TEMP_UNAVAILABLE.
func rejectReason(reason lease.RejectionReason) RejectReason {
	switch reason {
	case lease.RejectionDigestNotAllowed, lease.RejectionInvalidOffer:
		return RejectPolicy
	case lease.RejectionAlreadyActive:
		return RejectLocalBusy
	default: // draining, disconnected, expired, and anything added later
		return RejectTempUnavailable
	}
}
