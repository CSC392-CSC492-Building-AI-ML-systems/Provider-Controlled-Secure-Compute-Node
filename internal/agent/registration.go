package agent

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/coordinator"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/dashboard"
)

// Runtime names advertised to the coordinator. The coordinator matches them
// exactly against a job's required_runtime, so they must be agreed with
// Project 7b (R8). Until then these are our proposal.
const (
	// RuntimePodman: rootless Podman is installed, so curated OCI images can
	// run. Always advertised; registration fails without it.
	RuntimePodman = "podman"
	// RuntimeOllama: the provider allowlisted the curated Ollama inference image.
	// Not detectable on the host (Ollama runs inside the image), so the
	// provider declares it in config.
	RuntimeOllama = "ollama"
)

// CPUGPUModel is the gpu_model a CPU-only node registers with.
const CPUGPUModel = "cpu"

// ErrNoGPU means capability discovery found no GPU and CPU-only mode is off.
// Project 7b rejects vram_mb = 0 (R3), so the node cannot register.
var ErrNoGPU = errors.New("no GPU detected; set cpu_only in the node config to register as a CPU-only provider")

// RegistrationOptions are the provider's config choices that shape registration.
type RegistrationOptions struct {
	AcceptedTiers []string
	// ExtraRuntimes are advertised alongside detected ones, e.g. RuntimeOllama.
	ExtraRuntimes []string
	// CPUOnly lets a node with no GPU register, reporting system RAM as vram_mb.
	CPUOnly bool
}

// BuildRegistration reduces a capability report to the four fields Project 7b
// accepts (R4): gpu_model, vram_mb, driver_version, and runtimes.
//
// Only the first GPU is reported, since the coordinator models one GPU per
// provider. With no GPU, it returns ErrNoGPU unless opts.CPUOnly is set, in
// which case it registers gpu_model "cpu", vram_mb = system RAM, and
// driver_version "none".
func BuildRegistration(report dashboard.CapabilityReport, opts RegistrationOptions) (coordinator.Registration, error) {
	var caps coordinator.Capabilities
	switch {
	case len(report.GPUs) > 0:
		gpu := report.GPUs[0]
		for _, g := range report.GPUs[1:] {
			if g.Index < gpu.Index {
				gpu = g
			}
		}
		caps.GPUModel = strings.TrimSpace(gpu.Name)
		caps.VRAMMB = gpu.VRAMTotalMB
		caps.DriverVersion = strings.TrimSpace(report.Driver.Version)
	case opts.CPUOnly:
		caps.GPUModel = CPUGPUModel
		caps.VRAMMB = report.Host.RAMTotalMB
		caps.DriverVersion = "none"
	default:
		return coordinator.Registration{}, ErrNoGPU
	}
	if caps.GPUModel == "" {
		return coordinator.Registration{}, errors.New("capability report has a GPU with no model name")
	}
	if caps.VRAMMB <= 0 {
		return coordinator.Registration{}, fmt.Errorf("%s reports %d MB of memory; the coordinator requires vram_mb > 0", caps.GPUModel, caps.VRAMMB)
	}
	if caps.DriverVersion == "" {
		return coordinator.Registration{}, errors.New("capability report has no driver version")
	}

	// Every job runs in rootless Podman, so without it the node can run nothing
	// and advertising other runtimes would be false.
	if p := report.Runtimes.Podman; !p.Installed || !p.Rootless {
		return coordinator.Registration{}, errors.New("rootless Podman was not detected; the node cannot run jobs")
	}
	caps.Runtimes = runtimes(opts.ExtraRuntimes)
	if len(opts.AcceptedTiers) == 0 {
		return coordinator.Registration{}, errors.New("accepted_tiers is empty; the coordinator would never offer a job")
	}
	return coordinator.Registration{
		Capabilities:  caps,
		AcceptedTiers: append([]string(nil), opts.AcceptedTiers...),
	}, nil
}

// runtimes returns "podman" plus the configured extras, sorted and de-duplicated.
func runtimes(extra []string) []string {
	seen := map[string]bool{RuntimePodman: true}
	for _, name := range extra {
		if name = strings.TrimSpace(name); name != "" {
			seen[name] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
