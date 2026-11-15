package dashboard

import (
	"context"
	"time"
)

// StatusSource reports the agent's lifecycle state. Owned by the heartbeat-lease group.
type StatusSource interface {
	Status(ctx context.Context) (NodeStatus, error)
}

// CapabilitySource reports what the node has and how much of it is in use.
// Owned by the capability group.
type CapabilitySource interface {
	Capabilities(ctx context.Context) (CapabilityReport, error)
	Metrics(ctx context.Context) (Metrics, error)
}

// Controller carries out provider commands from the dashboard, the same ones
// gpu-ctl sends over the local socket. Owned by the heartbeat-lease group.
type Controller interface {
	Drain(ctx context.Context, grace time.Duration) error
	Reclaim(ctx context.Context) error
}
