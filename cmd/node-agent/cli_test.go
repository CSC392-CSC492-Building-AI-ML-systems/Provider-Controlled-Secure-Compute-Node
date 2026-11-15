package main

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPromptConfigEditSpecsAndStart(t *testing.T) {
	// 4=specs, 2=manual, set GPU + VRAM, back, back, start
	in := bufio.NewReader(strings.NewReader("4\n2\n1\nNVIDIA GeForce RTX 4080\n2\n16384\nb\nb\ns\n"))
	cfg, err := promptConfig(in, validCLIConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GPUModel != "NVIDIA GeForce RTX 4080" || cfg.VRAMMB != 16384 {
		t.Fatalf("specs=%q %d", cfg.GPUModel, cfg.VRAMMB)
	}
}

func TestPromptConfigQuit(t *testing.T) {
	_, err := promptConfig(bufio.NewReader(strings.NewReader("q\n")), Config{})
	if err == nil || err.Error() != "quit" {
		t.Fatalf("err=%v", err)
	}
}

func TestSplitCSV(t *testing.T) {
	got := splitCSV(" ollama , llama.cpp,,")
	if len(got) != 2 || got[0] != "ollama" || got[1] != "llama.cpp" {
		t.Fatalf("%v", got)
	}
}

func TestSpecsSummary(t *testing.T) {
	if specsSummary(Config{}) != "(unset)" {
		t.Fatal("empty")
	}
	s := specsSummary(Config{GPUModel: "RTX 4080", VRAMMB: 16, Driver: "580", Runtimes: []string{"ollama"}})
	if !strings.Contains(s, "RTX 4080") || !strings.Contains(s, "ollama") {
		t.Fatalf("%q", s)
	}
}

func TestPromptConfigExecutionSettings(t *testing.T) {
	want := validCLIConfig(t)
	in := bufio.NewReader(strings.NewReader("s\n1\n" + want.Allowlist + "\n2\n" + want.OfferPath + "\n3\ny\ns\n"))
	cfg, err := promptConfig(in, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Allowlist != want.Allowlist || cfg.OfferPath != want.OfferPath || !cfg.ShowOutput {
		t.Fatalf("config=%+v", cfg)
	}
}

func validCLIConfig(t *testing.T) Config {
	t.Helper()
	dir := t.TempDir()
	cfg := Config{Allowlist: filepath.Join(dir, "allowlist.json"), OfferPath: filepath.Join(dir, "offer.json")}
	for path, contents := range map[string]string{
		cfg.Allowlist: `{"images":[]}`,
		cfg.OfferPath: `{"ID":"lease-1","ImageDigest":"sha256:abc","ExpiresAt":"2099-01-01T00:00:00Z"}`,
	} {
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return cfg
}

func TestPromptConfigCorrectBadPath(t *testing.T) {
	cfg := validCLIConfig(t)
	goodPath := cfg.OfferPath
	cfg.OfferPath = filepath.Join(t.TempDir(), "missing.json")
	in := bufio.NewReader(strings.NewReader("s\n2\n" + goodPath + "\ns"))
	got, err := promptConfig(in, cfg)
	if err != nil || got.OfferPath != goodPath {
		t.Fatalf("config=%+v err=%v", got, err)
	}
}

func TestValidateInputs(t *testing.T) {
	for _, scenario := range []string{"directory", "bad-policy", "bad-offer", "unknown-field", "extra-offer", "oversized", "missing-fields", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := validCLIConfig(t)
			path, contents := cfg.OfferPath, "{"
			switch scenario {
			case "directory":
				cfg.OfferPath = t.TempDir()
			case "bad-policy":
				path = cfg.Allowlist
			case "unknown-field":
				contents = `{"unexpected":true}`
			case "extra-offer":
				contents = `{} {}`
			case "missing-fields":
				contents = `{}`
			case "expired":
				contents = `{"ID":"lease-1","ImageDigest":"sha256:abc","ExpiresAt":"2000-01-01T00:00:00Z"}`
			case "oversized":
				contents = `{}` + strings.Repeat(" ", 64*1024)
			}
			if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			if err := validateInputs(cfg); err == nil {
				t.Fatal("invalid inputs accepted")
			}
		})
	}
}

func TestEditIntRejectInvalidVRAM(t *testing.T) {
	for _, input := range []string{"-1\n", "abc\n", "999999999999999999999999\n"} {
		got, err := editInt(bufio.NewReader(strings.NewReader(input)), "VRAM MiB", 1024)
		if err != nil || got != 1024 {
			t.Fatalf("input=%q got=%d err=%v", input, got, err)
		}
	}
}

func TestReadLineEOF(t *testing.T) {
	in := bufio.NewReader(strings.NewReader(" q "))
	line, err := readLine(in)
	if line != "q" || err != nil {
		t.Fatalf("line=%q err=%v", line, err)
	}
	if _, err := readLine(in); !errors.Is(err, io.EOF) {
		t.Fatalf("err=%v", err)
	}
	if _, err := promptConfig(bufio.NewReader(strings.NewReader("")), Config{}); !errors.Is(err, io.EOF) {
		t.Fatalf("err=%v", err)
	}
}
