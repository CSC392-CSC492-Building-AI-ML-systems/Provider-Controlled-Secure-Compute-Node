package main

import (
	"flag"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/agent"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/coordinator"
)

// coordinatorFlags configures the optional coordinator connection used with
// -serve. Without -coordinator the agent runs on its own, as before.
type coordinatorFlags struct {
	url, providerID, apiKey string
	gpuModel, driver        string
	vramMB                  int
	runtimes, tiers         string
	interval, lostAfter     time.Duration
}

func registerCoordinatorFlags(flags *flag.FlagSet) *coordinatorFlags {
	host, _ := os.Hostname()
	c := &coordinatorFlags{}
	flags.StringVar(&c.url, "coordinator", "", "coordinator base URL, e.g. http://127.0.0.1:8000 (with -serve; empty runs without one)")
	flags.StringVar(&c.providerID, "provider-id", strings.ToLower(host), "provider ID; must be stable across restarts")
	flags.StringVar(&c.apiKey, "api-key", os.Getenv("NODE_API_KEY"), "coordinator API key (env NODE_API_KEY)")
	// Capabilities come from flags until capability discovery feeds registration.
	flags.StringVar(&c.gpuModel, "gpu-model", "", "GPU model reported to the coordinator")
	flags.IntVar(&c.vramMB, "vram-mb", 0, "total GPU VRAM in MiB reported to the coordinator")
	flags.StringVar(&c.driver, "driver", "", "NVIDIA driver version reported to the coordinator")
	flags.StringVar(&c.runtimes, "runtimes", "", "comma-separated runtimes, e.g. ollama")
	flags.StringVar(&c.tiers, "tiers", "", "comma-separated job tiers this provider accepts")
	flags.DurationVar(&c.interval, "heartbeat", 0, "heartbeat interval (default 5s)")
	flags.DurationVar(&c.lostAfter, "lost-after", 0, "stop taking new work after this long without a heartbeat (default 15s)")
	return c
}

// heartbeat builds the coordinator heartbeat, or returns nil when no
// coordinator is configured.
func (c *coordinatorFlags) heartbeat() *agent.Heartbeat {
	if c.url == "" {
		return nil
	}
	return &agent.Heartbeat{
		Client: coordinator.NewP16(c.url, c.providerID, c.apiKey),
		Registration: coordinator.Registration{
			Capabilities: coordinator.Capabilities{
				GPUModel:      c.gpuModel,
				VRAMMB:        c.vramMB,
				DriverVersion: c.driver,
				Runtimes:      splitList(c.runtimes),
			},
			AcceptedTiers: splitList(c.tiers),
		},
		Interval:  c.interval,
		LostAfter: c.lostAfter,
		Log:       slog.Default().With("provider_id", c.providerID),
	}
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
