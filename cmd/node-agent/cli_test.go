package main

import (
	"bufio"
	"strings"
	"testing"
)

func TestPromptConfigEditSpecsAndStart(t *testing.T) {
	// 4=specs, 2=manual, set GPU + VRAM, back, back, start
	in := bufio.NewReader(strings.NewReader("4\n2\n1\nNVIDIA GeForce RTX 4080\n2\n16384\nb\nb\ns\n"))
	cfg, err := promptConfig(in, Config{Allowlist: "allowlist.json", OfferPath: "offer.json"})
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
	in := bufio.NewReader(strings.NewReader("s\n1\npolicy.json\n2\noffer.json\n3\ny\ns\n"))
	cfg, err := promptConfig(in, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Allowlist != "policy.json" || cfg.OfferPath != "offer.json" || !cfg.ShowOutput {
		t.Fatalf("config=%+v", cfg)
	}
}
