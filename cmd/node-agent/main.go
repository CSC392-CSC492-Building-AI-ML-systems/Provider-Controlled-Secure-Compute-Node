// Command node-agent runs a single coordinator offer supplied as JSON on stdin,
// or, with -serve, stays running so gpu-ctl can start, drain and reclaim work.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/allowlist"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/containerengine"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/control"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

func main() {
	allowlistPath := flag.String("allowlist", "", "provider-owned JSON allowlist file (required)")
	showOutput := flag.Bool("show-output", false, "print bounded container logs to stderr after exit")
	serveMode := flag.Bool("serve", false, "keep running and take commands from gpu-ctl instead of one offer on stdin")
	socketPath := flag.String("socket", control.DefaultSocketPath(), "control socket used with -serve")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var workloadOutput io.Writer
	if *showOutput {
		workloadOutput = os.Stderr
	}
	if *serveMode {
		if err := serve(ctx, *allowlistPath, *socketPath, os.Stdout, workloadOutput, log.Default()); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := run(ctx, *allowlistPath, os.Stdin, os.Stdout, workloadOutput); err != nil {
		log.Fatal(err)
	}
}

// serve runs the lease manager until ctx is cancelled, taking commands from
// gpu-ctl on the control socket. Lease events are written to output as JSON
// lines. Stopping the agent cancels and removes any running workload.
func serve(ctx context.Context, path string, socketPath string, output io.Writer, workloadOutput io.Writer, logger *log.Logger) error {
	checker, err := allowlist.Load(path)
	if err != nil {
		return err
	}
	listener, err := control.Listen(socketPath)
	if err != nil {
		return err
	}
	recorder := control.NewRecorder(&localReporter{encoder: json.NewEncoder(output)})
	engine := containerengine.New(checker)
	engine.Output = workloadOutput
	manager := lease.NewManager(checker, engine, recorder)
	logger.Printf("node agent listening on %s with %d approved image(s); control it with gpu-ctl", socketPath, len(checker.Entries()))
	return control.NewServer(manager, recorder, checker.Entries(), logger).Serve(ctx, listener)
}

// This local handoff exercises the lease boundary without inventing the remote
// coordinator protocol. Stdout contains decisions/results, never workload output.
func run(ctx context.Context, path string, input io.Reader, output io.Writer, workloadOutput io.Writer) error {
	checker, err := allowlist.Load(path)
	if err != nil {
		return err
	}
	var offer lease.Offer
	decoder := json.NewDecoder(io.LimitReader(input, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&offer); err != nil {
		return fmt.Errorf("decode offer: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected exactly one JSON offer")
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

// localReporter writes lease events as JSON lines. Serve mode reports from
// several goroutines, so writes are serialized.
type localReporter struct {
	mu      sync.Mutex
	encoder *json.Encoder
}

func (reporter *localReporter) encode(event map[string]any) error {
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
	return reporter.encode(map[string]any{"type": "result", "lease_id": result.LeaseID, "outcome": result.Outcome, "exit_code": result.ExitCode})
}
