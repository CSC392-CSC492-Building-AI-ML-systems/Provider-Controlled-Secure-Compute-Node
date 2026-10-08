// Command node-agent runs the provider node's control loop.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/agent"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/coordinator"
)

func main() {
	host, _ := os.Hostname()
	url := flag.String("coordinator", "http://127.0.0.1:8000", "coordinator base URL")
	id := flag.String("id", strings.ToLower(host), "provider ID; must be stable across restarts")
	apiKey := flag.String("api-key", os.Getenv("NODE_API_KEY"), "coordinator API key (env NODE_API_KEY)")
	// Capabilities come from flags until capability discovery (M1) lands.
	gpu := flag.String("gpu-model", "", "GPU model, e.g. \"NVIDIA GeForce RTX 4080\"")
	vram := flag.Int("vram-mb", 0, "total GPU VRAM in MiB")
	driver := flag.String("driver", "", "NVIDIA driver version")
	runtimes := flag.String("runtimes", "", "comma-separated runtimes, e.g. ollama,llama.cpp")
	tiers := flag.String("tiers", "", "comma-separated job tiers this provider accepts")
	interval := flag.Duration("heartbeat", 0, "heartbeat interval (default 5s)")
	lostAfter := flag.Duration("lost-after", 0, "assume leases revoked after this long without a heartbeat (default 15s)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil)).With("provider_id", *id)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hb := &agent.Heartbeat{
		Client: coordinator.NewP16(*url, *id, *apiKey),
		Registration: coordinator.Registration{
			Capabilities: coordinator.Capabilities{
				GPUModel:      *gpu,
				VRAMMB:        *vram,
				DriverVersion: *driver,
				Runtimes:      split(*runtimes),
			},
			AcceptedTiers: split(*tiers),
		},
		Interval:  *interval,
		LostAfter: *lostAfter,
		// ponytail: logs only until the lease manager exists to stop running jobs here (H10).
		OnLost: func() { log.Warn("leases presumed revoked") },
		Log:    log,
	}
	if err := hb.Run(ctx); err != nil {
		log.Error("heartbeat stopped", "err", err)
		os.Exit(1)
	}
}

func split(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
