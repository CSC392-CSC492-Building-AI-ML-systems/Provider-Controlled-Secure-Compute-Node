package control

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/dashboard"
)

// IPCProbe verifies the local control socket permissions.
type IPCProbe struct {
	SocketPath string
}

// ID returns the dashboard catalog id.
func (IPCProbe) ID() string { return "ipc_socket" }

// Run checks that the control directory is 0700 and the socket is 0600.
func (p IPCProbe) Run(context.Context) dashboard.ProbeResult {
	if p.SocketPath == "" {
		return dashboard.ProbeResult{
			Status:  dashboard.ProbeFail,
			Message: "control socket path is empty",
		}
	}
	directory := filepath.Dir(p.SocketPath)
	if err := checkPrivateDirectory(directory); err != nil {
		return dashboard.ProbeResult{
			Status:  dashboard.ProbeFail,
			Message: err.Error(),
		}
	}
	info, err := os.Lstat(p.SocketPath)
	if err != nil {
		return dashboard.ProbeResult{
			Status:  dashboard.ProbeFail,
			Message: fmt.Sprintf("control socket missing: %v", err),
		}
	}
	if info.Mode()&os.ModeSocket == 0 {
		return dashboard.ProbeResult{
			Status:  dashboard.ProbeFail,
			Message: "control path is not a Unix socket",
		}
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		return dashboard.ProbeResult{
			Status:  dashboard.ProbeFail,
			Message: fmt.Sprintf("control socket mode %o, want 0600", perm),
		}
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Getuid() {
		return dashboard.ProbeResult{
			Status:  dashboard.ProbeFail,
			Message: "control socket is owned by another user",
		}
	}
	return dashboard.ProbeResult{
		Status:  dashboard.ProbePass,
		Message: fmt.Sprintf("control socket %s is owner-only (0600)", p.SocketPath),
	}
}
