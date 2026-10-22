package lease

import "context"

// ImageAllowlist decides whether an image digest may run on this node.
type ImageAllowlist interface {
	IsAllowed(ctx context.Context, imageDigest string) (bool, error)
}

// Workload is a running container workload.
type Workload interface {
	Wait(ctx context.Context) (exitCode int, err error)
	Kill(ctx context.Context) error
}

// ContainerEngine launches container workloads.
type ContainerEngine interface {
	Launch(ctx context.Context, imageDigest string) (Workload, error)
}

// Coordinator reports node-side lease decisions and outcomes.
type Coordinator interface {
	ReportAccepted(ctx context.Context, leaseID string) error
	ReportRejected(ctx context.Context, leaseID string, reason RejectionReason) error
	ReportResult(ctx context.Context, result Result) error
}
