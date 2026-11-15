package dashboard

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Probe is one check in the validation matrix. Each owning group registers its
// own implementation; see Catalog for the fixed set of ids.
type Probe interface {
	ID() string
	Run(ctx context.Context) ProbeResult
}

// ProbeInfo is the fixed description of a validation-matrix row.
type ProbeInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Requirement string `json:"requirement"`
	Layer       string `json:"layer"`
	Owner       string `json:"owner"`
}

// Catalog is every row of the validation matrix, in display order.
// It mirrors the probe table in docs/dashboard-api.md.
var Catalog = []ProbeInfo{
	{"overlay_network", "Encrypted Overlay Network", "Verify WireGuard node registration & IP allocation", "Tailscale/Headscale", "networking"},
	{"capability_discovery", "Capability Discovery", "Verify NVML parsing, driver version & CUDA memory reporting", "Provider Agent", "capability"},
	{"lease_heartbeat", "Lease & Heartbeat Manager", "Validate transitions IDLE -> BUSY -> DRAIN -> RECLAIM", "Host Daemon", "heartbeat-lease"},
	{"policy_gateway", "Security & Policy Gateway", "Validate container allowlist & pinned SHA-256 digest match", "Host Daemon", "container-security"},
	{"rootless_podman_cdi", "Rootless Podman + CDI", "Verify unprivileged CDI device passthrough (nvidia.com/gpu=all)", "Container Boundary", "capability"},
	{"privilege_dropping", "Kernel Privilege Dropping", "Enforce --cap-drop=ALL and --security-opt no-new-privileges", "Container Security", "container-security"},
	{"model_mount", "GGUF Model Storage Mount", "Verify read-only bind mount on /var/cache/models:/models:ro", "Filesystem Isolation", "capability"},
	{"lan_isolation", "Host LAN Network Isolation", "Block 192.168.0.0/16 & 10.0.0.0/8; permit overlay/loopback only", "Network Policy", "security-hardening"},
	{"ipc_socket", "Local IPC Provider Socket", "Verify /tmp/gpu-agent.sock permissions are 0600 (owner only)", "Local Ownership", "heartbeat-lease"},
}

// ProbeTimeout bounds a single probe run so a hung command cannot stall an audit.
const ProbeTimeout = 10 * time.Second

// Registry holds the probe implementations and each row's last result.
type Registry struct {
	mu     sync.Mutex
	probes map[string]Probe
	last   map[string]ProbeRow
}

func NewRegistry() *Registry {
	return &Registry{probes: map[string]Probe{}, last: map[string]ProbeRow{}}
}

// Register adds an implementation for one of the Catalog ids, replacing any previous one.
func (r *Registry) Register(p Probe) error {
	if _, ok := catalogInfo(p.ID()); !ok {
		return fmt.Errorf("dashboard: unknown probe id %q", p.ID())
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.probes[p.ID()] = p
	delete(r.last, p.ID())
	return nil
}

// Rows returns every Catalog row with its last result, without running anything.
func (r *Registry) Rows() []ProbeRow {
	r.mu.Lock()
	defer r.mu.Unlock()
	rows := make([]ProbeRow, 0, len(Catalog))
	for _, info := range Catalog {
		if row, ok := r.last[info.ID]; ok {
			rows = append(rows, row)
			continue
		}
		row := ProbeRow{ProbeInfo: info, Status: ProbeNotRun, Message: "not run yet"}
		if _, ok := r.probes[info.ID]; !ok {
			row.Status, row.Message = ProbeNotImplemented, "no implementation registered"
		}
		rows = append(rows, row)
	}
	return rows
}

// Run runs one probe. ok is false for an id not in the Catalog.
func (r *Registry) Run(ctx context.Context, id string) (row ProbeRow, ok bool) {
	info, ok := catalogInfo(id)
	if !ok {
		return ProbeRow{}, false
	}
	r.mu.Lock()
	p := r.probes[id]
	r.mu.Unlock()

	res := ProbeResult{Status: ProbeNotImplemented, Message: "no implementation registered"}
	if p != nil {
		res = runSafely(ctx, p)
	}
	now := time.Now().UTC()
	row = ProbeRow{ProbeInfo: info, Status: res.Status, Message: res.Message, CheckedAt: &now}

	r.mu.Lock()
	r.last[id] = row
	r.mu.Unlock()
	return row, true
}

// RunAll runs every Catalog probe in order.
func (r *Registry) RunAll(ctx context.Context) []ProbeRow {
	rows := make([]ProbeRow, 0, len(Catalog))
	for _, info := range Catalog {
		row, _ := r.Run(ctx, info.ID)
		rows = append(rows, row)
	}
	return rows
}

// runSafely turns a panic or a timeout inside a probe into a failed result.
func runSafely(ctx context.Context, p Probe) (res ProbeResult) {
	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	defer func() {
		if v := recover(); v != nil {
			res = ProbeResult{Status: ProbeFail, Message: fmt.Sprintf("probe panicked: %v", v)}
		}
	}()
	res = p.Run(ctx)
	if ctx.Err() != nil && res.Status != ProbeFail {
		return ProbeResult{Status: ProbeFail, Message: "probe timed out"}
	}
	return res
}

func catalogInfo(id string) (ProbeInfo, bool) {
	for _, info := range Catalog {
		if info.ID == id {
			return info, true
		}
	}
	return ProbeInfo{}, false
}
