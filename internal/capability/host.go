/*
Prompts:
	Go ahead and complete host.go according to the description
	Would the scripts ran work if the process is not root? Answer: its fine
*/

package capability

import (
	"bufio"
	"fmt"
	"io/fs"
	"runtime"
	"strconv"
	"strings"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/dashboard"
)

// DetectHostInfo reads host details from a filesystem. The filesystem root is
// expected to contain Linux paths such as proc/cpuinfo and etc/os-release.
func DetectHostInfo(fsys fs.FS) (dashboard.HostInfo, error) {
	cpuInfo, err := fs.ReadFile(fsys, "proc/cpuinfo")
	if err != nil {
		return dashboard.HostInfo{}, fmt.Errorf("read proc/cpuinfo: %w", err)
	}
	cpuModel, cpuCores, err := parseCPUInfo(cpuInfo)
	if err != nil {
		return dashboard.HostInfo{}, fmt.Errorf("parse proc/cpuinfo: %w", err)
	}

	memInfo, err := fs.ReadFile(fsys, "proc/meminfo")
	if err != nil {
		return dashboard.HostInfo{}, fmt.Errorf("read proc/meminfo: %w", err)
	}
	ramTotalMB, err := parseMemTotalMB(memInfo)
	if err != nil {
		return dashboard.HostInfo{}, fmt.Errorf("parse proc/meminfo: %w", err)
	}

	osRelease, err := fs.ReadFile(fsys, "etc/os-release")
	if err != nil {
		return dashboard.HostInfo{}, fmt.Errorf("read etc/os-release: %w", err)
	}
	osName, err := parseOSName(osRelease)
	if err != nil {
		return dashboard.HostInfo{}, fmt.Errorf("parse etc/os-release: %w", err)
	}

	kernel, err := fs.ReadFile(fsys, "proc/sys/kernel/osrelease")
	if err != nil {
		return dashboard.HostInfo{}, fmt.Errorf("read proc/sys/kernel/osrelease: %w", err)
	}
	kernelVersion := strings.TrimSpace(string(kernel))
	if kernelVersion == "" {
		return dashboard.HostInfo{}, fmt.Errorf("proc/sys/kernel/osrelease is empty")
	}

	return dashboard.HostInfo{
		CPUModel:   cpuModel,
		CPUCores:   cpuCores,
		RAMTotalMB: ramTotalMB,
		OS:         osName,
		Kernel:     kernelVersion,
		Arch:       runtime.GOARCH,
	}, nil
}

func parseCPUInfo(data []byte) (model string, cores int, err error) {
	processors := make(map[string]struct{})
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "model name", "Hardware", "Processor":
			if model == "" && value != "" {
				model = value
			}
		case "processor":
			if value != "" {
				processors[value] = struct{}{}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", 0, err
	}
	if model == "" {
		return "", 0, fmt.Errorf("CPU model not found")
	}
	if len(processors) == 0 {
		return "", 0, fmt.Errorf("CPU core count not found")
	}
	return model, len(processors), nil
}

func parseMemTotalMB(data []byte) (int, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ":")
		if !ok || strings.TrimSpace(key) != "MemTotal" {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) < 2 || fields[1] != "kB" {
			return 0, fmt.Errorf("invalid MemTotal value %q", strings.TrimSpace(value))
		}
		totalKB, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil || totalKB == 0 {
			return 0, fmt.Errorf("invalid MemTotal value %q", strings.TrimSpace(value))
		}
		return int(totalKB / 1024), nil
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("MemTotal not found")
}

func parseOSName(data []byte) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok || strings.TrimSpace(key) != "PRETTY_NAME" {
			continue
		}
		name := strings.TrimSpace(value)
		if len(name) >= 2 && name[0] == '"' && name[len(name)-1] == '"' {
			unquoted, err := strconv.Unquote(name)
			if err != nil {
				return "", fmt.Errorf("invalid PRETTY_NAME: %w", err)
			}
			name = unquoted
		} else if len(name) >= 2 && name[0] == '\'' && name[len(name)-1] == '\'' {
			name = name[1 : len(name)-1]
		}
		if name == "" {
			return "", fmt.Errorf("PRETTY_NAME is empty")
		}
		return name, nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("PRETTY_NAME not found")
}