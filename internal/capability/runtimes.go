/*
Prompts:
	Assume that podman and nvidia-ctk are installed on the system. Implement runtimes.go according to the description. Do not write a test file for it
	what is the result in the case that podman is not installed? Answer: the podman return values are both false for "installed" and "rootless"
*/

package capability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/dashboard"
)

/*
DetectRuntimes reports Podman and CDI availability.
A missing Podman binary is represented as Installed=false.
If nvidia-ctk is absent, CDI spec files are checked in the standard system directories.
*/
func DetectRuntimes(ctx context.Context) (dashboard.Runtimes, error) {
	podman, podmanErr := detectPodman(ctx)
	cdi, cdiErr := detectCDI(ctx)

	runtimes := dashboard.Runtimes{
		Podman: podman,
		CDI:    cdi,
	}
	return runtimes, errors.Join(podmanErr, cdiErr)
}

// detectPodman locates Podman, reads its host information, and reports its runtime details.
func detectPodman(ctx context.Context) (dashboard.PodmanInfo, error) {
	// Look for podman program
	path, err := exec.LookPath("podman")

	// Some error occured during lookup
	if err != nil {
		// Return empty PodmanInfo instance if podman program not found
		if errors.Is(err, exec.ErrNotFound) {
			return dashboard.PodmanInfo{}, nil
		}

		// Podman found but error on lookup: return empty podman lookup and resulting error
		return dashboard.PodmanInfo{}, fmt.Errorf("find podman executable: %w", err)
	}

	// Exec to get host info of podman program
	output, err := exec.CommandContext(ctx, path, "info", "--format", "json").Output()
	
	// If any error, return empty podman with resulting error
	if err != nil {
		return dashboard.PodmanInfo{}, fmt.Errorf("run podman info: %w", err)
	}

	// Parse info into temp for PodmanInfo struct
	var info struct {
		Version struct {
			Version string `json:"Version"`
		} `json:"version"`
		Host struct {
			OCIRuntime struct {
				Name string `json:"name"`
			} `json:"ociRuntime"`
			Security struct {
				Rootless *bool `json:"rootless"`
			} `json:"security"`
		} `json:"host"`
	}
	if err := json.Unmarshal(output, &info); err != nil {
		return dashboard.PodmanInfo{}, fmt.Errorf("parse podman info JSON: %w", err)
	}
	if strings.TrimSpace(info.Version.Version) == "" {
		return dashboard.PodmanInfo{}, fmt.Errorf("parse podman info JSON: version is missing")
	}
	if info.Host.Security.Rootless == nil {
		return dashboard.PodmanInfo{}, fmt.Errorf("parse podman info JSON: host.security.rootless is missing")
	}
	ociRuntime := strings.TrimSpace(info.Host.OCIRuntime.Name)
	if ociRuntime == "" {
		return dashboard.PodmanInfo{}, fmt.Errorf("parse podman info JSON: host.ociRuntime.name is missing")
	}
	
	return dashboard.PodmanInfo{
		Installed:  true,
		Version:    strings.TrimSpace(info.Version.Version),
		Rootless:   *info.Host.Security.Rootless,
		OCIRuntime: ociRuntime,
	}, nil
}

// detectCDI lists CDI devices with nvidia-ctk or checks standard CDI spec directories.
func detectCDI(ctx context.Context) (dashboard.CDIInfo, error) {
	path, err := exec.LookPath("nvidia-ctk")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return detectCDISpecFiles()
		}
		return dashboard.CDIInfo{}, fmt.Errorf("find nvidia-ctk executable: %w", err)
	}

	output, err := exec.CommandContext(ctx, path, "cdi", "list").Output()
	if err != nil {
		return dashboard.CDIInfo{}, fmt.Errorf("run nvidia-ctk cdi list: %w", err)
	}

	devices := parseCDIDeviceList(string(output))
	return dashboard.CDIInfo{
		Available: len(devices) > 0,
		Devices:   devices,
	}, nil
}

// parseCDIDeviceList returns unique, valid CDI device names in sorted order.
func parseCDIDeviceList(output string) []string {
	seen := make(map[string]struct{})
	for _, line := range strings.Split(output, "\n") {
		device := strings.TrimSpace(line)
		if strings.Count(device, "=") != 1 || strings.ContainsAny(device, " \t\r") {
			continue
		}
		parts := strings.SplitN(device, "=", 2)
		if parts[0] == "" || parts[1] == "" {
			continue
		}
		seen[device] = struct{}{}
	}
	return sortedKeys(seen)
}

// detectCDISpecFiles reports whether standard CDI directories contain a spec file.
func detectCDISpecFiles() (dashboard.CDIInfo, error) {
	paths := []string{"/etc/cdi", "/var/run/cdi"}
	found := false
	var readErrors []error
	for _, path := range paths {
		entries, err := os.ReadDir(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			readErrors = append(readErrors, fmt.Errorf("read CDI spec directory %s: %w", path, err))
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			switch strings.ToLower(filepath.Ext(entry.Name())) {
			case ".json", ".yaml", ".yml":
				found = true
			}
		}
	}
	return dashboard.CDIInfo{
		Available: found,
		Devices:   []string{},
	}, errors.Join(readErrors...)
}

// sortedKeys returns the keys from values in ascending order.
func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	sort.Strings(keys)
	return keys
}