package agent

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/coordinator"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/dashboard"
)

var rootlessPodman = dashboard.Runtimes{Podman: dashboard.PodmanInfo{Installed: true, Rootless: true, Version: "5.2.0"}}

// labReport is the full discovery report from a lab machine: far more than
// the coordinator accepts.
func labReport() dashboard.CapabilityReport {
	return dashboard.CapabilityReport{
		SchemaVersion: "1",
		GPUs: []dashboard.GPU{{
			Index: 0, UUID: "GPU-1234", Name: "NVIDIA GeForce RTX 4080", VRAMTotalMB: 16376,
			PCIBusID: "00000000:01:00.0", ComputeCapability: dashboard.ComputeCapability{Major: 8, Minor: 9},
		}},
		Driver:   dashboard.DriverInfo{Version: "580.178.04", CUDAVersion: "13.0"},
		Runtimes: dashboard.Runtimes{Podman: rootlessPodman.Podman, CDI: dashboard.CDIInfo{Available: true, Devices: []string{"nvidia.com/gpu=0"}}},
		Host:     dashboard.HostInfo{CPUModel: "Intel", CPUCores: 16, RAMTotalMB: 32000, Arch: "amd64"},
	}
}

func TestBuildRegistrationMapsToFourFields(t *testing.T) {
	got, err := BuildRegistration(labReport(), RegistrationOptions{
		AcceptedTiers: []string{"standard"},
		ExtraRuntimes: []string{RuntimeOllama, " ", RuntimePodman},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := coordinator.Registration{
		Capabilities: coordinator.Capabilities{
			GPUModel:      "NVIDIA GeForce RTX 4080",
			VRAMMB:        16376,
			DriverVersion: "580.178.04",
			Runtimes:      []string{"ollama", "podman"},
		},
		AcceptedTiers: []string{"standard"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestBuildRegistrationPicksLowestIndexGPU(t *testing.T) {
	report := labReport()
	report.GPUs = []dashboard.GPU{
		{Index: 1, Name: "NVIDIA RTX A4000", VRAMTotalMB: 16000},
		{Index: 0, Name: "NVIDIA GeForce RTX 3060", VRAMTotalMB: 12000},
	}
	got, err := BuildRegistration(report, RegistrationOptions{AcceptedTiers: []string{"t"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Capabilities.GPUModel != "NVIDIA GeForce RTX 3060" || got.Capabilities.VRAMMB != 12000 {
		t.Fatalf("got %+v", got.Capabilities)
	}
}

func TestBuildRegistrationWithoutGPU(t *testing.T) {
	cpu := dashboard.CapabilityReport{Runtimes: rootlessPodman, Host: dashboard.HostInfo{RAMTotalMB: 8192}}

	if _, err := BuildRegistration(cpu, RegistrationOptions{AcceptedTiers: []string{"t"}}); !errors.Is(err, ErrNoGPU) {
		t.Fatalf("without cpu_only: err=%v, want ErrNoGPU", err)
	}

	got, err := BuildRegistration(cpu, RegistrationOptions{AcceptedTiers: []string{"t"}, CPUOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	want := coordinator.Capabilities{GPUModel: "cpu", VRAMMB: 8192, DriverVersion: "none", Runtimes: []string{"podman"}}
	if !reflect.DeepEqual(got.Capabilities, want) {
		t.Fatalf("got %+v, want %+v", got.Capabilities, want)
	}

	// A GPU that is present wins over cpu_only.
	gpu, err := BuildRegistration(labReport(), RegistrationOptions{AcceptedTiers: []string{"t"}, CPUOnly: true})
	if err != nil || gpu.Capabilities.GPUModel != "NVIDIA GeForce RTX 4080" {
		t.Fatalf("got %+v err=%v", gpu.Capabilities, err)
	}
}

func TestBuildRegistrationRejects(t *testing.T) {
	tiers := RegistrationOptions{AcceptedTiers: []string{"t"}}
	tests := []struct {
		name   string
		report func(*dashboard.CapabilityReport)
		opts   RegistrationOptions
		want   string
	}{
		{"zero vram", func(r *dashboard.CapabilityReport) { r.GPUs[0].VRAMTotalMB = 0 }, tiers, "vram_mb > 0"},
		{"no model", func(r *dashboard.CapabilityReport) { r.GPUs[0].Name = " " }, tiers, "no model name"},
		{"no driver", func(r *dashboard.CapabilityReport) { r.Driver.Version = "" }, tiers, "no driver version"},
		{"no podman", func(r *dashboard.CapabilityReport) { r.Runtimes.Podman = dashboard.PodmanInfo{} }, tiers, "rootless Podman"},
		{"rootful podman", func(r *dashboard.CapabilityReport) { r.Runtimes.Podman.Rootless = false }, tiers, "rootless Podman"},
		{"no tiers", func(*dashboard.CapabilityReport) {}, RegistrationOptions{}, "accepted_tiers"},
		{"cpu_only with unknown RAM", func(r *dashboard.CapabilityReport) { r.GPUs = nil; r.Host.RAMTotalMB = 0 },
			RegistrationOptions{AcceptedTiers: []string{"t"}, CPUOnly: true}, "vram_mb > 0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := labReport()
			test.report(&report)
			_, err := BuildRegistration(report, test.opts)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err=%v, want it to mention %q", err, test.want)
			}
		})
	}
}
