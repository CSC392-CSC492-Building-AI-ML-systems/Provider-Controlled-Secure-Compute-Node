// Package containerengine provides the boundary to the local container runtime.
package containerengine

import (
	"context"
	"errors"

	"github.com/CSC392-CSC492-Building-AI-ML-systems/Provider-Controlled-Secure-Compute-Node/internal/lease"
)

// ErrNotImplemented marks the unfinished container-engine adapter.
var ErrNotImplemented = errors.New("container engine is not implemented")

// Engine is a placeholder for the rootless container-runtime adapter.
type Engine struct{}

// New creates an unwired container engine.
func New() *Engine {
	return &Engine{}
}

// Launch starts a digest-pinned workload.
func (engine *Engine) Launch(_ context.Context, _ string) (lease.Workload, error) {
	return nil, ErrNotImplemented
}

// Workload represents a container launched by Engine.
type Workload struct{}

// Wait waits for the workload to stop.
func (workload *Workload) Wait(_ context.Context) (int, error) {
	return 0, ErrNotImplemented
}

// Kill terminates the workload.
func (workload *Workload) Kill(_ context.Context) error {
	return ErrNotImplemented
}
