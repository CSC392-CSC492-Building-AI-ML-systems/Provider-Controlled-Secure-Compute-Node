package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

func TestOfferToPodmanToResult(t *testing.T) {
	for _, scenario := range []string{"completed", "failed-job", "pull-failed", "rejected"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			digest := "sha256:" + strings.Repeat("a", 64)
			policy := filepath.Join(dir, "allowlist.json")
			if err := os.WriteFile(policy, []byte(`{"images":[{"digest":"`+digest+`","image":"registry.example/team/job"}]}`), 0600); err != nil {
				t.Fatal(err)
			}
			script := `#!/bin/sh
[ "$1" = '--remote=false' ] || exit 99
printf '%s\n' "$2" >> "$PODMAN_TEST_LOG"
case "$2" in
info) echo true;;
pull) [ -s "$PODMAN_TEST_EVENTS" ] || exit 98
      [ "$PODMAN_TEST_SCENARIO" != 'pull-failed' ] || exit 125;;
wait) if [ "$PODMAN_TEST_SCENARIO" = 'failed-job' ]; then echo 17; else echo 0; fi;;
esac
`
			if err := os.WriteFile(filepath.Join(dir, "podman"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			t.Setenv("PODMAN_TEST_LOG", filepath.Join(dir, "calls"))
			t.Setenv("PODMAN_TEST_EVENTS", filepath.Join(dir, "events"))
			t.Setenv("PODMAN_TEST_SCENARIO", scenario)
			output, err := os.Create(filepath.Join(dir, "events"))
			if err != nil {
				t.Fatal(err)
			}
			defer output.Close()
			offer := lease.Offer{ID: "lease-1", ImageDigest: digest, ExpiresAt: time.Now().Add(time.Minute)}
			if scenario == "rejected" {
				offer.ImageDigest = "sha256:" + strings.Repeat("b", 64)
			}
			input, _ := json.Marshal(offer)
			err = run(context.Background(), policy, bytes.NewReader(input), output)
			if (err != nil) != (scenario == "pull-failed" || scenario == "rejected") {
				t.Fatalf("run error=%v", err)
			}
			contents, err := os.ReadFile(filepath.Join(dir, "events"))
			if err != nil {
				t.Fatal(err)
			}
			var events []map[string]any
			decoder := json.NewDecoder(bytes.NewReader(contents))
			for decoder.More() {
				var event map[string]any
				if err := decoder.Decode(&event); err != nil {
					t.Fatal(err)
				}
				events = append(events, event)
			}
			if scenario == "rejected" {
				if len(events) != 1 || events[0]["type"] != "rejected" {
					t.Fatalf("events=%v", events)
				}
				if _, err := os.Stat(filepath.Join(dir, "calls")); !os.IsNotExist(err) {
					t.Fatal("Podman called for rejected offer")
				}
				return
			}
			if len(events) != 2 || events[0]["type"] != "accepted" || events[1]["type"] != "result" {
				t.Fatalf("events=%v", events)
			}
			outcome := "completed"
			if scenario != "completed" {
				outcome = "failed"
			}
			if events[1]["outcome"] != outcome {
				t.Fatalf("result=%v", events[1])
			}
			calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
			stages := strings.Fields(string(calls))
			expected := []string{"info", "pull", "create", "start", "wait", "rm"}
			if scenario == "pull-failed" {
				expected = []string{"info", "pull"}
			}
			if !reflect.DeepEqual(stages, expected) {
				t.Fatalf("calls=%v", stages)
			}
		})
	}
}
