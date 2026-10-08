// Package coordinator defines what the node needs from a coordinator, in the
// node's own terms. Each coordinator API (e.g. P16) is an adapter that maps its
// wire format and status codes onto this interface and the errors below.
package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Client interface {
	Register(ctx context.Context, r Registration) error
	Heartbeat(ctx context.Context) error
	Offers(ctx context.Context) ([]Offer, error)
	// Accept returns the lease expiry; zero means the lease does not expire.
	Accept(ctx context.Context, leaseID string) (time.Time, error)
	Reject(ctx context.Context, leaseID string, reason RejectReason) error
	Started(ctx context.Context, leaseID string) error
	Renew(ctx context.Context, leaseID string, extend time.Duration) (time.Time, error)
	Report(ctx context.Context, leaseID string, r Result) error
	Drain(ctx context.Context, grace time.Duration) error
}

var (
	// ErrAlreadyRegistered: the coordinator already knows this provider ID.
	ErrAlreadyRegistered = errors.New("provider already registered")
	// ErrUnknownProvider: the coordinator forgot us (e.g. it restarted); re-register.
	ErrUnknownProvider = errors.New("coordinator does not know this provider")
	// ErrRemoved: the coordinator will not take us back (e.g. drained).
	ErrRemoved = errors.New("provider removed from federation")
	// ErrLeaseGone: the lease was revoked, expired, or never existed. Stop its work.
	ErrLeaseGone = errors.New("lease no longer held")
)

// APIError is a coordinator error that has no meaning to the node beyond being logged.
type APIError struct {
	Status        int
	Code, Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("coordinator: %d %s: %s", e.Status, e.Code, e.Message)
}

type Capabilities struct {
	GPUModel      string
	VRAMMB        int
	DriverVersion string
	Runtimes      []string
}

type Registration struct {
	Capabilities  Capabilities
	AcceptedTiers []string
}

type Offer struct {
	LeaseID string
	JobID   string
	// Requirements is kept raw: its shape is not agreed with any coordinator yet.
	Requirements json.RawMessage
}

type RejectReason string

const (
	RejectLocalBusy           RejectReason = "LOCAL_BUSY"
	RejectTempUnavailable     RejectReason = "TEMP_UNAVAILABLE"
	RejectPolicy              RejectReason = "POLICY_REJECT"
	RejectCapabilityMismatch  RejectReason = "CAPABILITY_MISMATCH"
	RejectResourceUnavailable RejectReason = "RESOURCE_UNAVAILABLE"
)

type FailureReason string

const (
	FailExecution          FailureReason = "EXECUTION_ERROR"
	FailRuntime            FailureReason = "RUNTIME_ERROR"
	FailCapabilityMismatch FailureReason = "CAPABILITY_MISMATCH"
	FailDiskFull           FailureReason = "DISK_FULL"
	FailArtifactUpload     FailureReason = "ARTIFACT_UPLOAD_FAILED"
)

// Result is a finished job. Reason is empty on success.
type Result struct {
	Success bool
	Reason  FailureReason
}
