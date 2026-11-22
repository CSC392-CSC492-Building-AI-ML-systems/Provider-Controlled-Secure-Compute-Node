// Package control is the local command path from gpu-ctl to a running node
// agent. It uses a Unix socket in a directory only the provider's user can
// open, so file permissions decide who may drain, reclaim or start work.
package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/allowlist"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/coordinator"
	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

// Commands understood by the node agent.
const (
	CommandStatus  = "status"
	CommandImages  = "images"
	CommandRun     = "run"
	CommandDrain   = "drain"
	CommandResume  = "resume"
	CommandReclaim = "reclaim"
)

// MaxRunDuration matches the container engine's one-hour lifetime cap.
const MaxRunDuration = time.Hour

const (
	maxRequestBytes  = 4096
	maxResponseBytes = 1 << 20
)

// Request is one command sent to the node agent.
type Request struct {
	Command string `json:"command"`
	// Digest selects the approved image for CommandRun.
	Digest string `json:"digest,omitempty"`
	// Seconds is the lease length for CommandRun.
	Seconds int `json:"seconds,omitempty"`
}

// Response is the node agent's reply. Every successful reply includes the
// node's status after the command.
type Response struct {
	OK                bool                       `json:"ok"`
	Error             string                     `json:"error,omitempty"`
	State             lease.State                `json:"state,omitempty"`
	CoordinatorStatus coordinator.ProviderStatus `json:"coordinator_status,omitempty"`
	Lease             *Lease                     `json:"lease,omitempty"`
	LastResult        *Result                    `json:"last_result,omitempty"`
	Images            []allowlist.Entry          `json:"images,omitempty"`
	// LeaseID is the lease started by CommandRun.
	LeaseID string `json:"lease_id,omitempty"`
}

// Lease describes the node's active lease.
type Lease struct {
	ID          string    `json:"id"`
	ImageDigest string    `json:"image_digest"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// Result describes the most recently finished lease.
type Result struct {
	LeaseID  string        `json:"lease_id"`
	Outcome  lease.Outcome `json:"outcome"`
	ExitCode *int          `json:"exit_code,omitempty"`
}

// DefaultSocketPath is where node-agent listens and gpu-ctl connects unless
// told otherwise: $XDG_RUNTIME_DIR/compute-node/control.sock, falling back
// to the user's temporary directory.
func DefaultSocketPath() string {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "compute-node", "control.sock")
}

// Listen creates the control socket. Its directory must be a real directory
// owned by the current user with mode 0700; it is created that way if missing.
// A leftover socket from a crashed agent is replaced, but a live one is not.
func Listen(path string) (net.Listener, error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create control directory: %w", err)
	}
	if err := checkPrivateDirectory(directory); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(path); err == nil {
		if connection, dialErr := net.DialTimeout("unix", path, time.Second); dialErr == nil {
			connection.Close()
			return nil, fmt.Errorf("another node agent is already listening on %s", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale control socket: %w", err)
		}
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on control socket: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		listener.Close()
		return nil, fmt.Errorf("restrict control socket: %w", err)
	}
	return listener, nil
}

func checkPrivateDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect control directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("control directory %s is not a directory (symlinks are refused)", directory)
	}
	if permissions := info.Mode().Perm(); permissions != 0o700 {
		return fmt.Errorf("control directory %s has mode %o, want 700", directory, permissions)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("control directory %s is owned by another user", directory)
	}
	return nil
}

// Send delivers one request to the node agent listening on socketPath. A reply
// with OK false is returned together with its error.
func Send(ctx context.Context, socketPath string, request Request) (Response, error) {
	var dialer net.Dialer
	connection, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
			return Response{}, fmt.Errorf("no node agent is listening on %s; start one with: node-agent -serve -allowlist <file>", socketPath)
		}
		return Response{}, fmt.Errorf("connect to node agent: %w", err)
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		connection.SetDeadline(deadline)
	}

	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return Response{}, fmt.Errorf("send command: %w", err)
	}
	var response Response
	if err := json.NewDecoder(io.LimitReader(connection, maxResponseBytes)).Decode(&response); err != nil {
		return Response{}, fmt.Errorf("read reply from node agent: %w", err)
	}
	if !response.OK {
		return response, errors.New(response.Error)
	}
	return response, nil
}
