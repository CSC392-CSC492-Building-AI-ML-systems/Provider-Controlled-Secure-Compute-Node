// Package config loads and validates the node-agent's provider configuration.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

// APIKeyEnv overrides api_key from the file, so the key need not be stored on disk.
const APIKeyEnv = "NODE_API_KEY"

const (
	DefaultHeartbeatInterval = 5 * time.Second
	DefaultLostAfter         = 15 * time.Second
)

// Config contains the external settings needed to wire the node agent.
// See docs/coordinator-requirements.md §6 for defaults.
type Config struct {
	// CoordinatorURL enables federation. Empty means local-only: no
	// registration or heartbeats, and gpu-ctl is the only source of work.
	CoordinatorURL string `json:"coordinator_url"`
	// ProviderID must be stable across restarts (R2). Defaults to the hostname.
	ProviderID string `json:"provider_id"`
	// APIKey is sent as X-API-Key. NODE_API_KEY overrides it.
	APIKey string `json:"api_key"`
	// AcceptedTiers are the job tiers this provider will run (R5).
	AcceptedTiers []string `json:"accepted_tiers"`
	// Runtimes are advertised in addition to the detected "podman" (R8).
	Runtimes []string `json:"runtimes"`
	// CPUOnly lets a node with no GPU register (R3), reporting system RAM.
	CPUOnly bool `json:"cpu_only"`
	// HeartbeatInterval is the time between heartbeats (H2).
	HeartbeatInterval Duration `json:"heartbeat_interval"`
	// LostAfter is how long without a heartbeat before leases are presumed revoked (H10).
	LostAfter Duration `json:"lost_after"`
	// AllowlistPath is the provider-owned image allowlist.
	AllowlistPath string `json:"allowlist"`
}

// Federated reports whether the node should register with a coordinator.
func (c Config) Federated() bool { return c.CoordinatorURL != "" }

// Duration is a time.Duration written in JSON as a Go duration string, e.g. "5s".
type Duration time.Duration

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string like \"5s\": %w", err)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

var providerIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// Load reads a JSON config file. An empty path loads only defaults and the
// environment, which gives a local-only node. Unknown fields are rejected so a
// typo cannot silently fall back to a default.
func Load(path string) (Config, error) {
	var cfg Config
	if path != "" {
		contents, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
		decoder := json.NewDecoder(bytes.NewReader(contents))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&cfg); err != nil {
			return Config{}, fmt.Errorf("parse config %s: %w", path, err)
		}
		if decoder.Decode(new(any)) != io.EOF {
			return Config{}, fmt.Errorf("parse config %s: expected one JSON object", path)
		}
	}
	if key := os.Getenv(APIKeyEnv); key != "" {
		cfg.APIKey = key
	}
	if cfg.ProviderID == "" {
		host, err := os.Hostname()
		if err != nil {
			return Config{}, fmt.Errorf("provider_id not set and hostname unavailable: %w", err)
		}
		cfg.ProviderID = strings.ToLower(host)
	}
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = Duration(DefaultHeartbeatInterval)
	}
	if cfg.LostAfter == 0 {
		cfg.LostAfter = Duration(DefaultLostAfter)
	}
	return cfg, cfg.validate()
}

func (c Config) validate() error {
	var errs []error
	if !providerIDPattern.MatchString(c.ProviderID) {
		errs = append(errs, fmt.Errorf("provider_id %q must be 1-63 lowercase letters, digits, '.', '_' or '-'", c.ProviderID))
	}
	if c.HeartbeatInterval <= 0 {
		errs = append(errs, errors.New("heartbeat_interval must be positive"))
	}
	if c.LostAfter <= c.HeartbeatInterval {
		errs = append(errs, fmt.Errorf("lost_after (%s) must be longer than heartbeat_interval (%s)",
			time.Duration(c.LostAfter), time.Duration(c.HeartbeatInterval)))
	}
	if c.Federated() {
		if !strings.HasPrefix(c.CoordinatorURL, "http://") && !strings.HasPrefix(c.CoordinatorURL, "https://") {
			errs = append(errs, fmt.Errorf("coordinator_url %q must start with http:// or https://", c.CoordinatorURL))
		}
		if len(c.AcceptedTiers) == 0 {
			errs = append(errs, errors.New("accepted_tiers must list at least one tier when coordinator_url is set"))
		}
		for _, tier := range c.AcceptedTiers {
			if strings.TrimSpace(tier) == "" {
				errs = append(errs, errors.New("accepted_tiers contains an empty tier"))
			}
		}
	}
	return errors.Join(errs...)
}
