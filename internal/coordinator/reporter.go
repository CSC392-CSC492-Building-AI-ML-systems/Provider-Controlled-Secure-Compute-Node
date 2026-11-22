package coordinator

import (
	"context"
	"fmt"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

var _ lease.Coordinator = (*LeaseReporter)(nil)

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

// ReportResult tells the coordinator how a lease ended.
func (r *LeaseReporter) ReportResult(ctx context.Context, result lease.Result) error {
	mapped, err := leaseResult(result)
	if err != nil {
		return err
	}
	return r.Client.Report(ctx, result.LeaseID, mapped)
}

// leaseResult maps a node outcome onto the coordinator's SUCCESS/FAILURE
// report (docs/coordinator-requirements.md L7, D4). A failure with an exit
// code means the job itself failed; without one, the node could not run or
// collect it. P16 has no reason for expiry or reclaim yet, so both are
// reported as RUNTIME_ERROR.
func leaseResult(result lease.Result) (Result, error) {
	switch result.Outcome {
	case lease.OutcomeCompleted:
		return Result{Success: true}, nil
	case lease.OutcomeFailed:
		if result.ExitCode != nil {
			return Result{Reason: FailExecution}, nil
		}
		return Result{Reason: FailRuntime}, nil
	case lease.OutcomeExpired, lease.OutcomeReclaimed:
		return Result{Reason: FailRuntime}, nil
	default:
		return Result{}, fmt.Errorf("lease %s: outcome %q cannot be reported as a result", result.LeaseID, result.Outcome)
	}
}
