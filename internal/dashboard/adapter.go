package dashboard

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

// LeaseManager is the subset of lease.Manager the live dashboard needs.
type LeaseManager interface {
	State() lease.State
	ActiveLease() (lease.Active, bool)
	Drain(ctx context.Context) error
	Reclaim(ctx context.Context) error
}

// AgentAdapter exposes a live lease manager as StatusSource and Controller.
// Heartbeat code can call SetCoordinatorStatus and NoteHeartbeat so the UI
// shows federation state without the dashboard importing the agent package.
type AgentAdapter struct {
	Manager    LeaseManager
	ProviderID string

	mu                sync.RWMutex
	coordinatorStatus string
	lastHeartbeatAt   *time.Time
}

var (
	_ StatusSource = (*AgentAdapter)(nil)
	_ Controller   = (*AgentAdapter)(nil)
)

// NewAgentAdapter wires the dashboard to a running lease manager.
func NewAgentAdapter(manager LeaseManager, providerID string) *AgentAdapter {
	if providerID == "" {
		providerID = "local-provider"
	}
	return &AgentAdapter{
		Manager:           manager,
		ProviderID:        providerID,
		coordinatorStatus: "UNREGISTERED",
	}
}

// Status reports the agent's lifecycle state for the dashboard API.
func (a *AgentAdapter) Status(context.Context) (NodeStatus, error) {
	if a == nil || a.Manager == nil {
		return NodeStatus{}, fmt.Errorf("lease manager is not configured")
	}
	st := NodeStatus{
		ProviderID:        a.ProviderID,
		State:             mapLeaseState(a.Manager.State()),
		CoordinatorStatus: a.CoordinatorStatus(),
	}
	if active, ok := a.Manager.ActiveLease(); ok {
		st.LeaseID = active.ID
	}
	a.mu.RLock()
	st.LastHeartbeatAt = a.lastHeartbeatAt
	a.mu.RUnlock()
	return st, nil
}

// Drain stops accepting new work. Grace is accepted for API compatibility;
// the lease manager drains immediately without a deadline timer.
func (a *AgentAdapter) Drain(ctx context.Context, _ time.Duration) error {
	if a == nil || a.Manager == nil {
		return fmt.Errorf("lease manager is not configured")
	}
	return a.Manager.Drain(ctx)
}

// Reclaim force-stops the active workload.
func (a *AgentAdapter) Reclaim(ctx context.Context) error {
	if a == nil || a.Manager == nil {
		return fmt.Errorf("lease manager is not configured")
	}
	return a.Manager.Reclaim(ctx)
}

// SetCoordinatorStatus records the federation view from heartbeat responses.
func (a *AgentAdapter) SetCoordinatorStatus(status string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	if status == "" {
		a.coordinatorStatus = "UNREGISTERED"
	} else {
		a.coordinatorStatus = status
	}
	a.mu.Unlock()
}

// NoteHeartbeat records a successful coordinator heartbeat time for status.
func (a *AgentAdapter) NoteHeartbeat(at time.Time) {
	if a == nil {
		return
	}
	a.mu.Lock()
	copied := at.UTC()
	a.lastHeartbeatAt = &copied
	a.mu.Unlock()
}

// CoordinatorStatus returns the last federation status string.
func (a *AgentAdapter) CoordinatorStatus() string {
	if a == nil {
		return "UNREGISTERED"
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.coordinatorStatus == "" {
		return "UNREGISTERED"
	}
	return a.coordinatorStatus
}

func mapLeaseState(state lease.State) AgentState {
	switch state {
	case lease.StateRunning:
		return StateBusy
	case lease.StateDraining:
		return StateDrain
	default:
		return StateIdle
	}
}

// EventReporter forwards lease reports to the next coordinator and logs them
// on the dashboard event bus for the diagnostic stream.
type EventReporter struct {
	Next lease.Coordinator
	Bus  *Bus
}

var _ lease.Coordinator = (*EventReporter)(nil)

// ReportAccepted implements lease.Coordinator.
func (r *EventReporter) ReportAccepted(ctx context.Context, leaseID string) error {
	r.log(LevelInfo, "lease", fmt.Sprintf("accepted lease %s", leaseID))
	if r.Next == nil {
		return nil
	}
	return r.Next.ReportAccepted(ctx, leaseID)
}

// ReportRejected implements lease.Coordinator.
func (r *EventReporter) ReportRejected(ctx context.Context, leaseID string, reason lease.RejectionReason) error {
	r.log(LevelWarn, "lease", fmt.Sprintf("rejected lease %s (%s)", leaseID, reason))
	if r.Next == nil {
		return nil
	}
	return r.Next.ReportRejected(ctx, leaseID, reason)
}

// ReportResult implements lease.Coordinator.
func (r *EventReporter) ReportResult(ctx context.Context, result lease.Result) error {
	level := LevelInfo
	if result.Outcome == lease.OutcomeFailed || result.Outcome == lease.OutcomeReclaimed {
		level = LevelWarn
	}
	r.log(level, "lease", fmt.Sprintf("lease %s outcome=%s", result.LeaseID, result.Outcome))
	if r.Next == nil {
		return nil
	}
	return r.Next.ReportResult(ctx, result)
}

func (r *EventReporter) log(level EventLevel, source, message string) {
	if r != nil && r.Bus != nil {
		r.Bus.Log(level, source, message)
	}
}
