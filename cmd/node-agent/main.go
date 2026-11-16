// Command node-agent runs a single coordinator offer supplied as JSON on stdin,
// or, with -serve, stays running so gpu-ctl can start, drain and reclaim work.
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

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/allowlist"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/containerengine"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/control"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

func main() {
	allowlistPath := flag.String("allowlist", "", "provider-owned JSON allowlist file (required)")
	showOutput := flag.Bool("show-output", false, "print bounded container logs to stderr after exit")
	interactive := flag.Bool("interactive", false, "configure a local workload through the interactive menu")
	flag.Parse()
	var input io.Reader = os.Stdin
	if *interactive {
		cfg, err := promptConfig(bufio.NewReader(os.Stdin), Config{Allowlist: *allowlistPath, ShowOutput: *showOutput})
		if err != nil {
			if errors.Is(err, errQuit) || errors.Is(err, io.EOF) {
				return
			}
			log.Fatal(err)
		}
		offerFile, err := os.Open(cfg.OfferPath)
		if err != nil {
			log.Fatal(err)
		}
		defer offerFile.Close()
		input = offerFile
		*allowlistPath, *showOutput = cfg.Allowlist, cfg.ShowOutput
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var workloadOutput io.Writer
	if *showOutput {
		workloadOutput = os.Stderr
	}
	if err := run(ctx, *allowlistPath, input, os.Stdout, workloadOutput); err != nil {
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
