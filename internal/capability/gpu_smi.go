// Package capability collects hardware capabilities and live resource metrics.
package capability

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/dashboard"
)

const (
	defaultGPUSMICommandTimeout = 5 * time.Second
	capabilitySchemaVersion     = "1"
)

// CommandRunner executes a command and returns its combined output.
//
// Keeping command execution behind this function makes the collector testable
// on hosts that do not have nvidia-smi installed.
type CommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// GPUSMICollector collects GPU capabilities and live metrics using nvidia-smi.
type GPUSMICollector struct {
	runner  CommandRunner
	timeout time.Duration
}

// NewGPUSMICollector creates a collector backed by the supplied command runner.
// A zero timeout selects the required five-second command timeout.
func NewGPUSMICollector(runner CommandRunner, timeout time.Duration) *GPUSMICollector {
	if timeout <= 0 {
		timeout = defaultGPUSMICommandTimeout
	}

	return &GPUSMICollector{
		runner:  runner,
		timeout: timeout,
	}
}

// Capabilities returns the GPUs and driver information reported by nvidia-smi.
func (c *GPUSMICollector) Capabilities(ctx context.Context) (dashboard.CapabilityReport, error) {
	plain, err := c.run(ctx) // runes the nvidia-smi command
	if err != nil {          // error checking for the previous command
		return dashboard.CapabilityReport{}, fmt.Errorf("run nvidia-smi: %w", err)
	}
	driver, cuda, err := parseSMIVersions(string(plain))
	if err != nil {
		return dashboard.CapabilityReport{}, err
	}

	query, err := c.run(ctx,
		"--query-gpu=index,uuid,name,memory.total,pci.bus_id,compute_cap,mig.mode.current",
		"--format=csv,noheader,nounits",
	)
	if err != nil {
		return dashboard.CapabilityReport{}, fmt.Errorf("query nvidia-smi GPUs: %w", err)
	}
	gpus, err := parseGPUQuery(query)
	if err != nil {
		return dashboard.CapabilityReport{}, err
	}

	return dashboard.CapabilityReport{
		SchemaVersion: capabilitySchemaVersion,
		CollectedAt:   time.Now(),
		GPUs:          gpus,
		Driver:        dashboard.DriverInfo{Version: driver, CUDAVersion: cuda},
		ProbeErrors:   []dashboard.ProbeError{},
	}, nil
}

func parseSMIVersions(output string) (driver, cuda string, err error) {
	for _, line := range strings.Split(output, "\n") {
		if i := strings.Index(line, "Driver Version:"); i >= 0 {
			fields := strings.Fields(line[i+len("Driver Version:"):]) // skipping over the keyword
			if len(fields) > 0 {
				driver = fields[0]
			}
		}
		if i := strings.Index(line, "CUDA Version:"); i >= 0 {
			fields := strings.Fields(line[i+len("CUDA Version:"):])
			if len(fields) > 0 {
				cuda = fields[0]
			}
		}
	}
	if driver == "" || cuda == "" {
		return "", "", errors.New("parse nvidia-smi: missing driver or CUDA version")
	}
	return driver, cuda, nil
}

func parseGPUQuery(output []byte) ([]dashboard.GPU, error) {
	r := csv.NewReader(strings.NewReader(string(output)))
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = 7
	var gpus []dashboard.GPU
	for {
		row, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse nvidia-smi GPU query: %w", err)
		}
		for i := range row {
			row[i] = strings.TrimSpace(row[i]) // cleaning each row
		}
		index, err := strconv.Atoi(row[0])
		if err != nil {
			return nil, fmt.Errorf("parse GPU index %q: %w", row[0], err)
		}
		memory, err := strconv.Atoi(row[3])
		if err != nil {
			return nil, fmt.Errorf("parse GPU memory %q: %w", row[3], err)
		}
		major, minor, err := parseComputeCapability(row[5])
		if err != nil {
			return nil, err
		}
		gpus = append(gpus, dashboard.GPU{
			Index: index, UUID: row[1], Name: row[2], VRAMTotalMB: memory,
			PCIBusID: row[4], ComputeCapability: dashboard.ComputeCapability{Major: major, Minor: minor},
			MIGEnabled: strings.EqualFold(row[6], "enabled"),
		})
	}
	if gpus == nil {
		gpus = []dashboard.GPU{}
	}
	return gpus, nil
}

func parseComputeCapability(value string) (int, int, error) {
	parts := strings.SplitN(value, ".", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("parse compute capability %q", value)
	}
	major, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("parse compute capability %q: %w", value, err)
	}
	minor, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("parse compute capability %q: %w", value, err)
	}
	return major, minor, nil
}

// Metrics returns the live per-GPU usage reported by nvidia-smi.
func (c *GPUSMICollector) Metrics(ctx context.Context) (dashboard.Metrics, error) {
	output, err := c.run(ctx,
		"--query-gpu=uuid,memory.used,memory.free,utilization.gpu,temperature.gpu",
		"--format=csv,noheader,nounits",
	)
	if err != nil {
		return dashboard.Metrics{}, fmt.Errorf("query nvidia-smi metrics: %w", err)
	}
	gpus, err := parseMetricsQuery(output)
	if err != nil {
		return dashboard.Metrics{}, err
	}
	return dashboard.Metrics{CollectedAt: time.Now(), GPUs: gpus}, nil
}

func parseMetricsQuery(output []byte) ([]dashboard.GPUMetrics, error) {
	r := csv.NewReader(strings.NewReader(string(output)))
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = 5
	var metrics []dashboard.GPUMetrics
	for {
		row, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse nvidia-smi metrics query: %w", err)
		}
		for i := range row {
			row[i] = strings.TrimSpace(row[i])
		}
		values := make([]float64, 4)
		for i := range values {
			values[i], err = parseMetricValue(row[i+1])
			if err != nil {
				return nil, fmt.Errorf("parse GPU metric %q: %w", row[i+1], err)
			}
		}
		metrics = append(metrics, dashboard.GPUMetrics{
			UUID: row[0], VRAMUsedMB: values[0], VRAMFreeMB: values[1],
			UtilizationPct: values[2], TemperatureC: values[3],
		})
	}
	if metrics == nil {
		metrics = []dashboard.GPUMetrics{}
	}
	return metrics, nil
}

// parseMetricValue converts an nvidia-smi metric to a number. nvidia-smi uses
// bracketed status values when a metric is unavailable or unsupported; the
// dashboard's numeric metric fields represent those values as zero.
func parseMetricValue(value string) (float64, error) {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "[n/a]", "[not supported]":
		return 0, nil
	default:
		return strconv.ParseFloat(value, 64)
	}
}

// run executes one nvidia-smi command with the collector's timeout.
func (c *GPUSMICollector) run(ctx context.Context, args ...string) ([]byte, error) {
	if c.runner == nil {
		return nil, errors.New("nvidia-smi command runner is nil")
	}

	commandCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	return c.runner(commandCtx, "nvidia-smi", args...)
}
