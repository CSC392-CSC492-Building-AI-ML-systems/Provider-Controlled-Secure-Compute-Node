// Package dashboard serves the node's local status API for the web dashboard.
// The wire format is docs/dashboard-api.md; change that document first.
package dashboard

import "time"

type AgentState string

const (
	StateIdle    AgentState = "IDLE"
	StateBusy    AgentState = "BUSY"
	StateDrain   AgentState = "DRAIN"
	StateReclaim AgentState = "RECLAIM"
)

type NodeStatus struct {
	ProviderID        string     `json:"provider_id"`
	State             AgentState `json:"state"`
	LeaseID           string     `json:"lease_id,omitempty"`
	CoordinatorStatus string     `json:"coordinator_status"`
	LastHeartbeatAt   *time.Time `json:"last_heartbeat_at,omitempty"`
	MeshIP            string     `json:"mesh_ip,omitempty"`
}

type CapabilityReport struct {
	SchemaVersion string       `json:"schema_version"`
	CollectedAt   time.Time    `json:"collected_at"`
	GPUs          []GPU        `json:"gpus"`
	Driver        DriverInfo   `json:"driver"`
	Runtimes      Runtimes     `json:"runtimes"`
	Host          HostInfo     `json:"host"`
	Models        []ModelInfo  `json:"models"`
	ProbeErrors   []ProbeError `json:"probe_errors"`
}

type GPU struct {
	Index             int               `json:"index"`
	UUID              string            `json:"uuid"`
	Name              string            `json:"name"`
	VRAMTotalMB       int               `json:"vram_total_mb"`
	PCIBusID          string            `json:"pci_bus_id"`
	ComputeCapability ComputeCapability `json:"compute_capability"`
	MIGEnabled        bool              `json:"mig_enabled"`
}

type ComputeCapability struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
}

type DriverInfo struct {
	Version     string `json:"version"`
	CUDAVersion string `json:"cuda_version"`
}

type Runtimes struct {
	Podman PodmanInfo `json:"podman"`
	CDI    CDIInfo    `json:"cdi"`
}

type PodmanInfo struct {
	Installed  bool   `json:"installed"`
	Version    string `json:"version,omitempty"`
	Rootless   bool   `json:"rootless"`
	OCIRuntime string `json:"oci_runtime,omitempty"`
}

type CDIInfo struct {
	Available bool     `json:"available"`
	Devices   []string `json:"devices"`
}

type HostInfo struct {
	CPUModel   string `json:"cpu_model"`
	CPUCores   int    `json:"cpu_cores"`
	RAMTotalMB int    `json:"ram_total_mb"`
	OS         string `json:"os"`
	Kernel     string `json:"kernel"`
	Arch       string `json:"arch"`
}

type ModelInfo struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	SizeMB int    `json:"size_mb"`
}

// ProbeError is a capability detection step that failed; the rest of the report still holds.
type ProbeError struct {
	Probe string `json:"probe"`
	Error string `json:"error"`
}

type Metrics struct {
	CollectedAt time.Time    `json:"collected_at"`
	GPUs        []GPUMetrics `json:"gpus"`
}

type GPUMetrics struct {
	UUID           string  `json:"uuid"`
	VRAMUsedMB     float64 `json:"vram_used_mb"`
	VRAMFreeMB     float64 `json:"vram_free_mb"`
	UtilizationPct float64 `json:"utilization_pct"`
	TemperatureC   float64 `json:"temperature_c"`
}

type ProbeStatus string

const (
	ProbePass           ProbeStatus = "pass"
	ProbeWarn           ProbeStatus = "warn"
	ProbeFail           ProbeStatus = "fail"
	ProbeNotImplemented ProbeStatus = "not_implemented"
	ProbeNotRun         ProbeStatus = "not_run"
)

type ProbeResult struct {
	Status  ProbeStatus
	Message string
}

// ProbeRow is one validation-matrix row: the probe's fixed description plus its last result.
type ProbeRow struct {
	ProbeInfo
	Status    ProbeStatus `json:"status"`
	Message   string      `json:"message"`
	CheckedAt *time.Time  `json:"checked_at,omitempty"`
}

type EventLevel string

const (
	LevelInfo  EventLevel = "info"
	LevelWarn  EventLevel = "warn"
	LevelError EventLevel = "error"
)

type Event struct {
	Time    time.Time  `json:"time"`
	Level   EventLevel `json:"level"`
	Source  string     `json:"source"`
	Message string     `json:"message"`
}
