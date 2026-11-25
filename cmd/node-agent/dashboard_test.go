package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/config"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/control"
)

func TestServeDashboardStatusOnLoopback(t *testing.T) {
	dir := t.TempDir()
	digest := "sha256:" + strings.Repeat("b", 64)
	policy := filepath.Join(dir, "allowlist.json")
	if err := os.WriteFile(policy, []byte(`{"images":[{"digest":"`+digest+`","image":"registry.example/team/job"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	socketDir, err := os.MkdirTemp("", "dash-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(socketDir)
	socket := filepath.Join(socketDir, "agent", "control.sock")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() {
		served <- serve(ctx, config.Config{AllowlistPath: policy, ProviderID: "dash-node"}, socket, io.Discard, nil, log.New(io.Discard, "", 0), dashboardOpts{
			Addr:       addr,
			WebDir:     filepath.Join(dir, "missing-web"),
			ProviderID: "dash-node",
		})
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := control.Send(context.Background(), socket, control.Request{Command: control.CommandStatus})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("agent did not start: %v / %v", err, <-served)
		}
		time.Sleep(20 * time.Millisecond)
	}

	var status map[string]any
	for {
		resp, err := http.Get("http://" + addr + "/api/status")
		if err == nil {
			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				t.Fatalf("status code = %d", resp.StatusCode)
			}
			err = json.NewDecoder(resp.Body).Decode(&status)
			resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("dashboard not ready: %v / %v", err, <-served)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status["provider_id"] != "dash-node" || status["state"] != "IDLE" {
		t.Fatalf("status = %#v", status)
	}

	drain, err := http.Post("http://"+addr+"/api/control/drain", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	drain.Body.Close()
	if drain.StatusCode != http.StatusAccepted {
		t.Fatalf("drain status = %d", drain.StatusCode)
	}

	cancel()
	if err := <-served; err != nil {
		t.Fatalf("serve: %v", err)
	}
}

func TestRefuseNonLoopbackDashboard(t *testing.T) {
	dir := t.TempDir()
	policy := filepath.Join(dir, "allowlist.json")
	if err := os.WriteFile(policy, []byte(`{"images":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "agent", "control.sock")
	err := serve(context.Background(), config.Config{AllowlistPath: policy}, socket, io.Discard, nil, log.New(io.Discard, "", 0), dashboardOpts{
		Addr: "0.0.0.0:8080",
	})
	if err == nil || !strings.Contains(err.Error(), "non-loopback") {
		t.Fatalf("err = %v, want non-loopback refusal", err)
	}
}
