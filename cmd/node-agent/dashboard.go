package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/allowlist"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/capability"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/control"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/dashboard"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

// dashboardOpts configures the optional loopback dashboard HTTP server.
type dashboardOpts struct {
	Addr       string
	WebDir     string
	ProviderID string
}

func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// startDashboard mounts the live dashboard API (and optional web UI) on a
// loopback address. It returns the adapter so heartbeat status can update it.
func startDashboard(
	ctx context.Context,
	opts dashboardOpts,
	manager *lease.Manager,
	checker *allowlist.Checker,
	socketPath string,
	bus *dashboard.Bus,
	logger *log.Logger,
) (*dashboard.AgentAdapter, error) {
	if opts.Addr == "" {
		return nil, nil
	}
	if !isLoopbackAddr(opts.Addr) {
		return nil, fmt.Errorf("refusing non-loopback dashboard address %q", opts.Addr)
	}
	adapter := dashboard.NewAgentAdapter(manager, opts.ProviderID)
	runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}
	caps := capability.NewGPUSMICollector(runner, 0)
	reg := dashboard.NewRegistry()
	probes := []dashboard.Probe{
		capability.CapabilityDiscoveryProbe{Collect: caps.Capabilities},
		capability.RootlessPodmanCDIProbe{DetectRuntimes: capability.DetectRuntimes},
		capability.ModelMountProbe{},
		allowlist.PolicyProbe{Checker: checker},
		control.IPCProbe{SocketPath: socketPath},
		leaseStateProbe{Manager: manager},
	}
	for _, probe := range probes {
		if err := reg.Register(probe); err != nil {
			return nil, err
		}
	}
	srv := &dashboard.Server{
		Status:  adapter,
		Caps:    caps,
		Control: adapter,
		Probes:  reg,
		Events:  bus,
	}
	if fi, err := os.Stat(opts.WebDir); err == nil && fi.IsDir() {
		srv.Static = http.FileServer(http.Dir(opts.WebDir))
	} else {
		logger.Printf("no built web app at %s; serving dashboard API only", opts.WebDir)
	}
	hs := &http.Server{
		Addr:              opts.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(shutdownCtx)
	}()
	go func() {
		logger.Printf("dashboard listening on http://%s", opts.Addr)
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Printf("dashboard stopped: %v", err)
		}
	}()
	bus.Log(dashboard.LevelInfo, "dashboard", "live dashboard attached to lease manager")
	return adapter, nil
}
