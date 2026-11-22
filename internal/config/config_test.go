package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "node.json")
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFederated(t *testing.T) {
	t.Setenv(APIKeyEnv, "")
	cfg, err := Load(writeConfig(t, `{
		"coordinator_url": "http://100.64.0.1:8000",
		"provider_id": "dh2020pc08",
		"api_key": "from-file",
		"accepted_tiers": ["standard"],
		"runtimes": ["ollama"],
		"heartbeat_interval": "2s",
		"lost_after": "10s",
		"allowlist": "/etc/node/allowlist.json"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Federated() || cfg.ProviderID != "dh2020pc08" || cfg.APIKey != "from-file" ||
		time.Duration(cfg.HeartbeatInterval) != 2*time.Second || time.Duration(cfg.LostAfter) != 10*time.Second ||
		cfg.AcceptedTiers[0] != "standard" || cfg.Runtimes[0] != "ollama" || cfg.AllowlistPath != "/etc/node/allowlist.json" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv(APIKeyEnv, "from-env")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	if cfg.Federated() || cfg.ProviderID != strings.ToLower(host) || cfg.APIKey != "from-env" ||
		time.Duration(cfg.HeartbeatInterval) != DefaultHeartbeatInterval || time.Duration(cfg.LostAfter) != DefaultLostAfter {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadEnvOverridesFileKey(t *testing.T) {
	t.Setenv(APIKeyEnv, "from-env")
	cfg, err := Load(writeConfig(t, `{"provider_id": "n1", "api_key": "from-file"}`))
	if err != nil || cfg.APIKey != "from-env" {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
}

func TestLoadRejects(t *testing.T) {
	tests := map[string]struct{ body, want string }{
		"unknown field":         {`{"provider_id": "n1", "heartbeat": "5s"}`, "unknown field"},
		"bad duration":          {`{"provider_id": "n1", "heartbeat_interval": "soon"}`, "invalid duration"},
		"numeric duration":      {`{"provider_id": "n1", "heartbeat_interval": 5}`, "duration must be a string"},
		"lost before beat":      {`{"provider_id": "n1", "heartbeat_interval": "10s", "lost_after": "5s"}`, "lost_after"},
		"bad provider id":       {`{"provider_id": "Has Spaces"}`, "provider_id"},
		"federated, no tiers":   {`{"provider_id": "n1", "coordinator_url": "http://x"}`, "accepted_tiers"},
		"federated, empty tier": {`{"provider_id": "n1", "coordinator_url": "http://x", "accepted_tiers": [""]}`, "empty tier"},
		"bad url":               {`{"provider_id": "n1", "coordinator_url": "x:8000", "accepted_tiers": ["t"]}`, "coordinator_url"},
		"two objects":           {`{"provider_id": "n1"} {}`, "one JSON object"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeConfig(t, test.body))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err=%v, want it to mention %q", err, test.want)
			}
		})
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("missing file: want error")
	}
}
