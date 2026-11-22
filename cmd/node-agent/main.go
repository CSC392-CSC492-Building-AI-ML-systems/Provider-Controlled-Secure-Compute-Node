// Command node-agent runs a single coordinator offer supplied as JSON on stdin,
// or, with -serve, stays running so gpu-ctl can start, drain and reclaim work
// and, when configured, registers and heartbeats with the coordinator.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/agent"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/allowlist"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/config"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/containerengine"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/control"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/coordinator"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

func main() {
	configPath := flag.String("config", "", "node config JSON file (coordinator, provider_id, tiers, heartbeat); omit for local-only")
	allowlistPath := flag.String("allowlist", "", "provider-owned JSON allowlist file (required unless set in -config)")
	showOutput := flag.Bool("show-output", false, "print bounded container logs to stderr after exit")
	interactive := flag.Bool("interactive", false, "configure a local workload through the interactive menu")
	serveMode := flag.Bool("serve", false, "keep the agent running for local gpu-ctl commands and coordinator heartbeats")
	socketPath := flag.String("socket", control.DefaultSocketPath(), "local control socket (with -serve)")
	flag.Parse()
	if *serveMode && *interactive {
		log.Fatal("-serve and -interactive cannot be used together")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	if *allowlistPath != "" {
		cfg.AllowlistPath = *allowlistPath
	}
	var input io.Reader = os.Stdin
	if *interactive {
		menu, err := promptConfig(bufio.NewReader(os.Stdin), Config{Allowlist: cfg.AllowlistPath, ShowOutput: *showOutput})
		if err != nil {
			if errors.Is(err, errQuit) || errors.Is(err, io.EOF) {
				return
			}
			log.Fatal(err)
		}
		offerFile, err := os.Open(menu.OfferPath)
		if err != nil {
			log.Fatal(err)
		}
		defer offerFile.Close()
		input = offerFile
		cfg.AllowlistPath, *showOutput = menu.Allowlist, menu.ShowOutput
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var workloadOutput io.Writer
	if *showOutput {
		workloadOutput = os.Stderr
	}
	if *serveMode {
		if err := serve(ctx, cfg, *socketPath, os.Stdout, workloadOutput, log.Default()); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := run(ctx, cfg.AllowlistPath, input, os.Stdout, workloadOutput); err != nil {
		log.Fatal(err)
	}
}

// discover is the capability discovery serve registers with; tests replace it.
var discover discoverFunc = discoverCapabilities

// serve runs the lease manager until ctx is cancelled, taking commands from
// gpu-ctl on the control socket. Lease events are written to output as JSON
// lines. Stopping the agent cancels and removes any running workload.
//
// When cfg names a coordinator, serve also registers and heartbeats on its
// own goroutine (H3). The node refuses leases until registration succeeds
// (R1), and serve fails if the node cannot register as configured.
func serve(ctx context.Context, cfg config.Config, socketPath string, output io.Writer, workloadOutput io.Writer, logger *log.Logger) error {
	checker, err := allowlist.Load(cfg.AllowlistPath)
	if err != nil {
		return err
	}
	recorder := control.NewRecorder(&localReporter{encoder: json.NewEncoder(output)})
	engine := containerengine.New(checker)
	engine.Output = workloadOutput
	manager := lease.NewManager(checker, engine, recorder)
	server := control.NewServer(manager, recorder, checker.Entries(), logger)

	var heartbeat *agent.Heartbeat
	if cfg.Federated() {
		client := coordinator.NewP16(cfg.CoordinatorURL, cfg.ProviderID, cfg.APIKey)
		if heartbeat, err = newHeartbeat(ctx, cfg, client, manager, discover, logger); err != nil {
			return err
		}
	}

	listener, err := control.Listen(socketPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	heartbeatDone := make(chan struct{})
	if heartbeat != nil {
		logger.Printf("heartbeating to %s every %s", cfg.CoordinatorURL, time.Duration(cfg.HeartbeatInterval))
		go func() {
			defer close(heartbeatDone)
			// A rejected registration stops the node. Removal by the
			// coordinator (ErrRemoved, H6) currently stops it too.
			if err := heartbeat.Run(ctx); err != nil && ctx.Err() == nil {
				cancel(fmt.Errorf("coordinator: %w", err))
			}
		}()
	} else {
		close(heartbeatDone)
	}
	logger.Printf("node agent listening on %s with %d approved image(s); control it with gpu-ctl", socketPath, len(checker.Entries()))
	err = server.Serve(ctx, listener)
	<-heartbeatDone
	if cause := context.Cause(ctx); err == nil && cause != nil && !errors.Is(cause, context.Canceled) {
		return cause
	}
	return err
}

// This local handoff exercises the lease boundary without inventing the remote
// coordinator protocol. Stdout contains decisions/results, never workload output.
func run(ctx context.Context, path string, input io.Reader, output io.Writer, workloadOutput io.Writer) error {
	checker, err := allowlist.Load(path)
	if err != nil {
		return err
	}
	offer, err := decodeOffer(input)
	if err != nil {
		return err
	}
	reporter := &localReporter{encoder: json.NewEncoder(output)}
	engine := containerengine.New(checker)
	engine.Output = workloadOutput
	manager := lease.NewManager(checker, engine, reporter)
	if err := manager.ReceiveOffer(ctx, offer); err != nil {
		return err
	}
	return manager.Wait(ctx)
}

func decodeOffer(input io.Reader) (lease.Offer, error) {
	var offer lease.Offer
	contents, err := io.ReadAll(io.LimitReader(input, 64*1024+1))
	if err != nil {
		return offer, fmt.Errorf("read offer: %w", err)
	}
	if len(contents) > 64*1024 {
		return offer, fmt.Errorf("offer exceeds 64 KiB limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&offer); err != nil {
		return offer, fmt.Errorf("decode offer: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return offer, fmt.Errorf("expected exactly one JSON offer")
	}
	return offer, nil
}

type localReporter struct {
	encoder *json.Encoder
	mu      sync.Mutex
}

func (reporter *localReporter) encode(event any) error {
	if reporter.encoder == nil {
		return errors.New("local reporter encoder is not configured")
	}
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	return reporter.encoder.Encode(event)
}

func (reporter *localReporter) ReportAccepted(_ context.Context, id string) error {
	return reporter.encode(map[string]any{"type": "accepted", "lease_id": id})
}
func (reporter *localReporter) ReportRejected(_ context.Context, id string, reason lease.RejectionReason) error {
	return reporter.encode(map[string]any{"type": "rejected", "lease_id": id, "reason": reason})
}
func (reporter *localReporter) ReportResult(_ context.Context, result lease.Result) error {
	return reporter.encode(map[string]any{
		"type":      "result",
		"lease_id":  result.LeaseID,
		"outcome":   result.Outcome,
		"exit_code": result.ExitCode,
		"inference": result.Inference,
	})
}
