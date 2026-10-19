// Package lease defines the node-side lease lifecycle.
package lease

import "time"

// State describes the lease manager's current lifecycle state.
type State string

const (
	StateIdle     State = "idle"
	StateRunning  State = "running"
	StateDraining State = "draining"
)

// Outcome describes a terminal lease result reported to the coordinator.
type Outcome string

const (
	OutcomeCompleted Outcome = "completed"
	OutcomeFailed    Outcome = "failed"
	OutcomeExpired   Outcome = "expired"
	OutcomeReclaimed Outcome = "reclaimed"
	OutcomeRejected  Outcome = "rejected"
)

// RejectionReason explains why the node declined a lease offer.
type RejectionReason string

const (
	RejectionDigestNotAllowed RejectionReason = "digest_not_allowed"
	RejectionDraining         RejectionReason = "draining"
	RejectionAlreadyActive    RejectionReason = "already_active"
	RejectionDisconnected     RejectionReason = "disconnected"
)

// Offer is the coordinator's request to run a digest-pinned image.
type Offer struct {
	ID          string
	ImageDigest string
	ExpiresAt   time.Time
}

// Active is the node's record of an accepted lease.
type Active struct {
	ID          string
	ImageDigest string
	ExpiresAt   time.Time
}

// Result is the terminal status sent to the coordinator.
type Result struct {
	LeaseID  string
	Outcome  Outcome
	ExitCode *int
}
