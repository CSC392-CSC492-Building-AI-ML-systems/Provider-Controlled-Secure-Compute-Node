// Command dashboard-mock serves the dashboard API with mock data, so the web
// UI can be built before the real agent subsystems exist.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/dashboard"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address; must be loopback")
	webDir := flag.String("web", "web/dist", "built web app to serve at /, skipped if missing")
	interval := flag.Duration("heartbeat", 5*time.Second, "mock heartbeat interval")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if !isLoopback(*addr) {
		log.Error("refusing to listen on a non-loopback address; the dashboard can drain and reclaim the node", "addr", *addr)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	node := dashboard.NewMockNode()
	bus := dashboard.NewBus()
	reg := dashboard.NewRegistry()
	for _, p := range dashboard.MockProbes() {
		if err := reg.Register(p); err != nil {
			log.Error("register probe", "err", err)
			os.Exit(1)
		}
	}
	srv := &dashboard.Server{Status: node, Caps: node, Control: node, Probes: reg, Events: bus}
	if fi, err := os.Stat(*webDir); err == nil && fi.IsDir() {
		srv.Static = http.FileServer(http.Dir(*webDir))
	} else {
		log.Info("no built web app, serving the API only", "web", *webDir)
	}

	bus.Log(dashboard.LevelInfo, "agent", "mock node started")
	go node.Simulate(ctx, bus, *interval)

	hs := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		// Request contexts end on shutdown, which closes open event streams.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hs.Shutdown(shutdownCtx)
	}()

	log.Info("dashboard mock API listening", "url", "http://"+*addr)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func isLoopback(addr string) bool {
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
