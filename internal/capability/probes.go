/*
Prompts:
	Implement probes.go according to the description
	
*/

package capability

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/dashboard"
)

const modelCachePath = "var/cache/models"

// CapabilityReportCollector collects the capability report used by the
// capability-discovery probe.
type CapabilityReportCollector func(context.Context) (dashboard.CapabilityReport, error)

// RuntimeDetector detects the Podman and CDI runtimes for the host.
type RuntimeDetector func(context.Context) (dashboard.Runtimes, error)

// CapabilityDiscoveryProbe passes when capability collection completes without
// returning errors or reporting individual probe errors.
type CapabilityDiscoveryProbe struct {
	Collect CapabilityReportCollector
}

func (CapabilityDiscoveryProbe) ID() string { return "capability_discovery" }

func (p CapabilityDiscoveryProbe) Run(ctx context.Context) dashboard.ProbeResult {
	if p.Collect == nil {
		return dashboard.ProbeResult{
			Status:  dashboard.ProbeFail,
			Message: "capability report collector is not configured",
		}
	}

	report, err := p.Collect(ctx)
	if err != nil {
		return dashboard.ProbeResult{
			Status:  dashboard.ProbeFail,
			Message: fmt.Sprintf("capability report collection failed: %v", err),
		}
	}
	if len(report.ProbeErrors) > 0 {
		messages := make([]string, 0, len(report.ProbeErrors))
		for _, probeErr := range report.ProbeErrors {
			messages = append(messages, fmt.Sprintf("%s: %s", probeErr.Probe, probeErr.Error))
		}
		return dashboard.ProbeResult{
			Status:  dashboard.ProbeFail,
			Message: "capability report contains errors: " + strings.Join(messages, "; "),
		}
	}
	return dashboard.ProbeResult{
		Status:  dashboard.ProbePass,
		Message: fmt.Sprintf("capability report collected successfully; %d GPU(s) detected", len(report.GPUs)),
	}
}

// RootlessPodmanCDIProbe passes only when Podman is installed and rootless and
// at least one CDI device/spec is available.
type RootlessPodmanCDIProbe struct {
	DetectRuntimes RuntimeDetector
}

func (RootlessPodmanCDIProbe) ID() string { return "rootless_podman_cdi" }

func (p RootlessPodmanCDIProbe) Run(ctx context.Context) dashboard.ProbeResult {
	detect := p.DetectRuntimes
	if detect == nil {
		detect = DetectRuntimes
	}
	runtimes, err := detect(ctx)
	if err != nil {
		return dashboard.ProbeResult{
			Status:  dashboard.ProbeFail,
			Message: fmt.Sprintf("runtime detection failed: %v", err),
		}
	}
	if !runtimes.Podman.Installed {
		return dashboard.ProbeResult{
			Status:  dashboard.ProbeFail,
			Message: "Podman not installed",
		}
	}
	if !runtimes.Podman.Rootless {
		return dashboard.ProbeResult{
			Status:  dashboard.ProbeFail,
			Message: "Podman is installed but is not running rootless",
		}
	}
	if !runtimes.CDI.Available {
		return dashboard.ProbeResult{
			Status:  dashboard.ProbeFail,
			Message: "no CDI spec or devices found",
		}
	}
	return dashboard.ProbeResult{
		Status:  dashboard.ProbePass,
		Message: fmt.Sprintf("Podman %s is rootless and CDI is available", runtimes.Podman.Version),
	}
}

// ModelMountProbe passes when /var/cache/models exists as a readable directory.
// Its filesystem can be replaced to test the check without using the host.
type ModelMountProbe struct {
	FS fs.FS
}

func (ModelMountProbe) ID() string { return "model_mount" }

func (p ModelMountProbe) Run(context.Context) dashboard.ProbeResult {
	fsys := p.FS
	if fsys == nil {
		fsys = os.DirFS("/")
	}
	_, err := fs.ReadDir(fsys, modelCachePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return dashboard.ProbeResult{
				Status:  dashboard.ProbeFail,
				Message: "/var/cache/models does not exist",
			}
		}
		return dashboard.ProbeResult{
			Status:  dashboard.ProbeFail,
			Message: fmt.Sprintf("/var/cache/models is not readable: %v", err),
		}
	}
	return dashboard.ProbeResult{
		Status:  dashboard.ProbePass,
		Message: "/var/cache/models exists and is readable",
	}
}