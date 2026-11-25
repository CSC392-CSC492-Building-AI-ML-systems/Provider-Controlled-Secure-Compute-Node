package dashboard

import (
	"context"
	"testing"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

type stubManager struct {
	state  lease.State
	active *lease.Active
}

func (s *stubManager) State() lease.State { return s.state }
func (s *stubManager) ActiveLease() (lease.Active, bool) {
	if s.active == nil {
		return lease.Active{}, false
	}
	return *s.active, true
}
func (s *stubManager) Drain(context.Context) error {
	s.state = lease.StateDraining
	return nil
}
func (s *stubManager) Reclaim(context.Context) error {
	s.state = lease.StateIdle
	s.active = nil
	return nil
}

func TestAgentAdapterStatusAndControls(t *testing.T) {
	manager := &stubManager{
		state:  lease.StateRunning,
		active: &lease.Active{ID: "lease-1"},
	}
	adapter := NewAgentAdapter(manager, "provider-a")
	st, err := adapter.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateBusy || st.LeaseID != "lease-1" || st.ProviderID != "provider-a" {
		t.Fatalf("status = %+v", st)
	}
	if st.CoordinatorStatus != "UNREGISTERED" {
		t.Fatalf("coordinator_status = %q, want UNREGISTERED", st.CoordinatorStatus)
	}

	adapter.SetCoordinatorStatus("ACTIVE")
	adapter.NoteHeartbeat(time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC))
	st, err = adapter.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.CoordinatorStatus != "ACTIVE" || st.LastHeartbeatAt == nil {
		t.Fatalf("after heartbeat status = %+v", st)
	}

	if err := adapter.Drain(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
	st, _ = adapter.Status(context.Background())
	if st.State != StateDrain {
		t.Fatalf("after drain state = %s", st.State)
	}
	if err := adapter.Reclaim(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, _ = adapter.Status(context.Background())
	if st.State != StateIdle || st.LeaseID != "" {
		t.Fatalf("after reclaim status = %+v", st)
	}
}

func TestAgentAdapterDefaultProviderID(t *testing.T) {
	adapter := NewAgentAdapter(&stubManager{state: lease.StateIdle}, "")
	st, err := adapter.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.ProviderID != "local-provider" {
		t.Fatalf("provider_id = %q", st.ProviderID)
	}
}

func TestMapLeaseState(t *testing.T) {
	cases := map[lease.State]AgentState{
		lease.StateIdle:     StateIdle,
		lease.StateRunning:  StateBusy,
		lease.StateDraining: StateDrain,
		lease.State("other"): StateIdle,
	}
	for in, want := range cases {
		if got := mapLeaseState(in); got != want {
			t.Errorf("mapLeaseState(%q) = %q, want %q", in, got, want)
		}
	}
}

type recordingCoordinator struct {
	accepted string
	rejected string
	result   lease.Result
}

func (r *recordingCoordinator) ReportAccepted(_ context.Context, id string) error {
	r.accepted = id
	return nil
}
func (r *recordingCoordinator) ReportRejected(_ context.Context, id string, _ lease.RejectionReason) error {
	r.rejected = id
	return nil
}
func (r *recordingCoordinator) ReportResult(_ context.Context, result lease.Result) error {
	r.result = result
	return nil
}

func TestEventReporterLogsAndForwards(t *testing.T) {
	bus := NewBus()
	next := &recordingCoordinator{}
	reporter := &EventReporter{Next: next, Bus: bus}

	if err := reporter.ReportAccepted(context.Background(), "l1"); err != nil {
		t.Fatal(err)
	}
	if next.accepted != "l1" {
		t.Fatalf("accepted = %q", next.accepted)
	}
	if err := reporter.ReportRejected(context.Background(), "l2", lease.RejectionDraining); err != nil {
		t.Fatal(err)
	}
	if next.rejected != "l2" {
		t.Fatalf("rejected = %q", next.rejected)
	}
	if err := reporter.ReportResult(context.Background(), lease.Result{LeaseID: "l3", Outcome: lease.OutcomeCompleted}); err != nil {
		t.Fatal(err)
	}
	if next.result.LeaseID != "l3" {
		t.Fatalf("result = %+v", next.result)
	}

	history, _, cancel := bus.Subscribe()
	defer cancel()
	if len(history) < 3 {
		t.Fatalf("expected bus events, got %#v", history)
	}
}
