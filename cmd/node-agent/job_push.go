package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/allowlist"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/jobpush"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

type jobPushOptions struct {
	addr         string
	tlsCert      string
	tlsKey       string
	ollamaDigest string
	requireGPU   bool
}

func registerJobPushFlags(flags *flag.FlagSet) *jobPushOptions {
	opts := new(jobPushOptions)
	flags.StringVar(&opts.addr, "job-listen", "", "authenticated requester API address (with -serve)")
	flags.StringVar(&opts.tlsCert, "job-tls-cert", "", "TLS certificate PEM for the requester API")
	flags.StringVar(&opts.tlsKey, "job-tls-key", "", "TLS private key PEM for the requester API")
	flags.StringVar(&opts.ollamaDigest, "job-ollama-digest", "", "allowlisted digest for requester Ollama prompt jobs")
	flags.BoolVar(&opts.requireGPU, "job-require-gpu", false, "require NVIDIA GPU inference for requester Ollama jobs")
	return opts
}

func (opts jobPushOptions) validate(serveMode bool) error {
	if opts.addr == "" {
		if opts != (jobPushOptions{}) {
			return errors.New("requester API options require -job-listen")
		}
		return nil
	}
	if !serveMode {
		return errors.New("-job-listen requires -serve")
	}
	if (opts.tlsCert == "") != (opts.tlsKey == "") {
		return errors.New("-job-tls-cert and -job-tls-key must be set together")
	}
	host, _, err := net.SplitHostPort(opts.addr)
	if err != nil {
		return fmt.Errorf("invalid -job-listen address: %w", err)
	}
	if opts.tlsCert == "" && !jobpush.IsLoopbackIP(host) {
		return errors.New("-job-listen requires TLS outside a literal loopback address")
	}
	if opts.requireGPU && opts.ollamaDigest == "" {
		return errors.New("-job-require-gpu requires -job-ollama-digest")
	}
	return jobpush.ValidateToken(os.Getenv(jobpush.TokenEnv))
}

func startJobPush(ctx context.Context, opts jobPushOptions, manager *lease.Manager, checker *allowlist.Checker, reporter *jobpush.Reporter, logger *log.Logger) (func(), error) {
	if opts.addr == "" {
		return func() {}, nil
	}
	if opts.ollamaDigest != "" {
		if _, err := checker.ImageReference(ctx, opts.ollamaDigest); err != nil {
			return nil, fmt.Errorf("-job-ollama-digest: %w", err)
		}
	}
	server := &http.Server{
		Handler:           jobpush.Handler(manager, reporter, os.Getenv(jobpush.TokenEnv), opts.ollamaDigest, opts.requireGPU, logger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	if opts.tlsCert != "" {
		certificate, err := tls.LoadX509KeyPair(opts.tlsCert, opts.tlsKey)
		if err != nil {
			return nil, fmt.Errorf("load requester API TLS certificate: %w", err)
		}
		server.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	}
	listener, err := net.Listen("tcp", opts.addr)
	if err != nil {
		return nil, fmt.Errorf("listen for requester jobs: %w", err)
	}
	go func() {
		scheme := "http"
		if server.TLSConfig != nil {
			scheme = "https"
		}
		logger.Printf("requester job API listening on %s://%s", scheme, listener.Addr())
		var err error
		if server.TLSConfig == nil {
			err = server.Serve(listener)
		} else {
			err = server.ServeTLS(listener, "", "")
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Printf("requester job API stopped: %v", err)
		}
	}()
	return func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Printf("requester job API shutdown: %v", err)
			_ = server.Close()
		}
	}, nil
}
