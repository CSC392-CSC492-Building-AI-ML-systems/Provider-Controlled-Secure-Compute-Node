package main

import (
	"context"
	"fmt"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/dashboard"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

// leaseStateProbe reports whether the lease manager is in a known lifecycle state.
type leaseStateProbe struct {
	Manager *lease.Manager
}

func (leaseStateProbe) ID() string { return "lease_heartbeat" }

func (p leaseStateProbe) Run(context.Context) dashboard.ProbeResult {
	if p.Manager == nil {
		return dashboard.ProbeResult{
			Status:  dashboard.ProbeFail,
			Message: "lease manager is not configured",
		}
	}
	state := p.Manager.State()
	switch state {
	case lease.StateIdle, lease.StateRunning, lease.StateDraining:
	default:
		return dashboard.ProbeResult{
			Status:  dashboard.ProbeFail,
			Message: fmt.Sprintf("unknown lease state %q", state),
		}
	}
	message := fmt.Sprintf("lease manager state is %s", state)
	if active, ok := p.Manager.ActiveLease(); ok {
		message = fmt.Sprintf("%s; active lease %s", message, active.ID)
	}
	return dashboard.ProbeResult{
		Status:  dashboard.ProbePass,
		Message: message,
	}
}
