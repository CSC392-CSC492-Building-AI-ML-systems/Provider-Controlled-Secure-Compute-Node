package dashboard

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"
)

const mockGPUUUID = "GPU-8a1f2c3d-0000-1111-2222-333344445555"

// MockNode stands in for the real agent until each group wires in its part.
// It implements StatusSource, CapabilitySource and Controller.
type MockNode struct {
	mu      sync.Mutex
	state   AgentState
	leaseID string
	lastHB  time.Time
	leaseNo int
}

var (
	_ StatusSource     = (*MockNode)(nil)
	_ CapabilitySource = (*MockNode)(nil)
	_ Controller       = (*MockNode)(nil)
)

func NewMockNode() *MockNode {
	return &MockNode{state: StateIdle, lastHB: time.Now().UTC()}
}

func (m *MockNode) Status(ctx context.Context) (NodeStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	hb := m.lastHB
	coord := "ACTIVE"
	if m.state == StateDrain || m.state == StateReclaim {
		coord = "DRAINING"
	}
	return NodeStatus{
		ProviderID:        "mock-node",
		State:             m.state,
		LeaseID:           m.leaseID,
		CoordinatorStatus: coord,
		LastHeartbeatAt:   &hb,
		MeshIP:            "100.64.0.12",
	}, nil
}

func (m *MockNode) Capabilities(ctx context.Context) (CapabilityReport, error) {
	return CapabilityReport{
		SchemaVersion: "1",
		CollectedAt:   time.Now().UTC(),
		GPUs: []GPU{{
			Index:             0,
			UUID:              mockGPUUUID,
			Name:              "NVIDIA GeForce RTX 4080",
			VRAMTotalMB:       16376,
			PCIBusID:          "00000000:01:00.0",
			ComputeCapability: ComputeCapability{Major: 8, Minor: 9},
		}},
		Driver: DriverInfo{Version: "550.54.14", CUDAVersion: "12.4"},
		Runtimes: Runtimes{
			Podman: PodmanInfo{Installed: true, Version: "4.9.3", Rootless: true, OCIRuntime: "crun"},
			CDI:    CDIInfo{Available: true, Devices: []string{"nvidia.com/gpu=0", "nvidia.com/gpu=all"}},
		},
		Host: HostInfo{
			CPUModel:   "Intel(R) Core(TM) i7-12700",
			CPUCores:   20,
			RAMTotalMB: 31842,
			OS:         "Ubuntu 22.04.4 LTS",
			Kernel:     "6.5.0-35-generic",
			Arch:       "amd64",
		},
		ProbeErrors: []ProbeError{},
	}, nil
}

func (m *MockNode) Metrics(ctx context.Context) (Metrics, error) {
	m.mu.Lock()
	busy := m.state == StateBusy
	m.mu.Unlock()

	used, util := 1432, 3
	if busy {
		used, util = 9800, 85
	}
	used += rand.IntN(200)
	util += rand.IntN(10)
	return Metrics{
		CollectedAt: time.Now().UTC(),
		GPUs: []GPUMetrics{{
			UUID:           mockGPUUUID,
			VRAMUsedMB:     float64(used),
			VRAMFreeMB:     float64(16376 - used),
			UtilizationPct: float64(util),
			TemperatureC:   float64(40 + util/4),
		}},
	}, nil
}

func (m *MockNode) Drain(ctx context.Context, grace time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == StateDrain || m.state == StateReclaim {
		return errors.New("node is already draining")
	}
	m.state = StateDrain
	return nil
}

func (m *MockNode) Reclaim(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == StateReclaim {
		return errors.New("node is already reclaimed")
	}
	m.state, m.leaseID = StateReclaim, ""
	return nil
}

// Simulate sends fake heartbeats to bus every interval and cycles the node
// between IDLE and BUSY so the dashboard has something moving. It returns when
// ctx is cancelled.
func (m *MockNode) Simulate(ctx context.Context, bus *Bus, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for tick := 1; ; tick++ {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		m.mu.Lock()
		m.lastHB = time.Now().UTC()
		var msg string
		if tick%4 == 0 {
			switch m.state {
			case StateIdle:
				m.leaseNo++
				m.state, m.leaseID = StateBusy, fmt.Sprintf("lease-mock-%04d", m.leaseNo)
				msg = "accepted lease " + m.leaseID
			case StateBusy:
				msg = "lease " + m.leaseID + " completed, reported SUCCESS"
				m.state, m.leaseID = StateIdle, ""
			}
		}
		m.mu.Unlock()

		bus.Log(LevelInfo, "heartbeat", "heartbeat ok")
		if msg != "" {
			bus.Log(LevelInfo, "lease", msg)
		}
	}
}

type mockProbe struct {
	id  string
	res ProbeResult
}

func (p mockProbe) ID() string                      { return p.id }
func (p mockProbe) Run(context.Context) ProbeResult { return p.res }

// MockProbes returns fixed results covering every status, so the validation
// matrix renders each state. Ids not listed stay not_implemented.
func MockProbes() []Probe {
	return []Probe{
		mockProbe{"capability_discovery", ProbeResult{ProbePass, "[mock] 1 GPU detected, driver 550.54.14, CUDA 12.4"}},
		mockProbe{"lease_heartbeat", ProbeResult{ProbePass, "[mock] state transitions valid"}},
		mockProbe{"rootless_podman_cdi", ProbeResult{ProbePass, "[mock] podman 4.9.3 rootless, CDI nvidia.com/gpu=all present"}},
		mockProbe{"ipc_socket", ProbeResult{ProbeWarn, "[mock] socket is 0660, expected 0600"}},
		mockProbe{"lan_isolation", ProbeResult{ProbeFail, "[mock] container reached 192.168.1.1"}},
	}
}
