package allowlist

import (
	"context"
	"fmt"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/dashboard"
)

// PolicyProbe checks that a digest allowlist is loaded for the policy gateway.
type PolicyProbe struct {
	Checker *Checker
}

// ID returns the dashboard catalog id.
func (PolicyProbe) ID() string { return "policy_gateway" }

// Run verifies the allowlist has at least one approved digest.
func (p PolicyProbe) Run(context.Context) dashboard.ProbeResult {
	if p.Checker == nil {
		return dashboard.ProbeResult{
			Status:  dashboard.ProbeFail,
			Message: "allowlist checker is not configured",
		}
	}
	entries := p.Checker.Entries()
	if len(entries) == 0 {
		return dashboard.ProbeResult{
			Status:  dashboard.ProbeWarn,
			Message: "allowlist is empty; every offer will be rejected",
		}
	}
	return dashboard.ProbeResult{
		Status:  dashboard.ProbePass,
		Message: fmt.Sprintf("allowlist loaded with %d approved digest(s)", len(entries)),
	}
}
