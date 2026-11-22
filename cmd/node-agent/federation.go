package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/agent"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/capability"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/config"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/coordinator"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/dashboard"
)

// connectivity is the part of lease.Manager that registration gates.
type connectivity interface {
	SetCoordinatorConnected(connected bool)
}

// discoverFunc collects the capability report used to register.
type discoverFunc func(ctx context.Context) dashboard.CapabilityReport

// discoverCapabilities runs nvidia-smi capability discovery. Without
// nvidia-smi (a CPU-only machine) it still detects runtimes and host RAM, so
// a cpu_only node can register.
func discoverCapabilities(ctx context.Context) dashboard.CapabilityReport {
	runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}
	report, err := capability.NewGPUSMICollector(runner, 0).Capabilities(ctx)
	if err == nil {
		return report
	}
	runtimes, _ := capability.DetectRuntimes(ctx)
	host, _ := capability.DetectHostInfo(os.DirFS("/"))
	return dashboard.CapabilityReport{
		Runtimes:    runtimes,
		Host:        host,
		ProbeErrors: []dashboard.ProbeError{{Probe: "gpu_smi", Error: err.Error()}},
	}
}

// newHeartbeat discovers capabilities, builds the registration payload, and
// gates the lease manager on registration (R1). It fails, and the node
// refuses to start, when the node cannot register as configured (e.g. no GPU
// and cpu_only unset).
func newHeartbeat(ctx context.Context, cfg config.Config, client coordinator.Client, manager connectivity, discover discoverFunc, logger *log.Logger) (*agent.Heartbeat, error) {
	report := discover(ctx)
	registration, err := agent.BuildRegistration(report, agent.RegistrationOptions{
		AcceptedTiers: cfg.AcceptedTiers,
		ExtraRuntimes: cfg.Runtimes,
		CPUOnly:       cfg.CPUOnly,
	})
	if err != nil {
		for _, probeErr := range report.ProbeErrors {
			logger.Printf("capability probe %s: %s", probeErr.Probe, probeErr.Error)
		}
		return nil, fmt.Errorf("cannot register with coordinator: %w", err)
	}
	caps := registration.Capabilities
	logger.Printf("registering as %s: gpu_model=%q vram_mb=%d driver=%s runtimes=%v tiers=%v",
		cfg.ProviderID, caps.GPUModel, caps.VRAMMB, caps.DriverVersion, caps.Runtimes, registration.AcceptedTiers)

	// Refuse leases until registration succeeds (R1).
	manager.SetCoordinatorConnected(false)
	registered := false
	return &agent.Heartbeat{
		Client:       client,
		Registration: registration,
		Interval:     time.Duration(cfg.HeartbeatInterval),
		LostAfter:    time.Duration(cfg.LostAfter),
		OnRegistered: func() {
			if !registered {
				registered = true
				manager.SetCoordinatorConnected(true)
			}
		},
		OnLost:      func() { manager.SetCoordinatorConnected(false) },
		OnRecovered: func() { manager.SetCoordinatorConnected(true) },
		Log:         slog.New(slog.NewTextHandler(logger.Writer(), nil)).With("provider_id", cfg.ProviderID),
	}, nil
}
