# Way forward

The provider-side lease and Podman path is implemented and tested with fakes.
Registration and heartbeat handling are wired. Lease offer polling and
coordinator result reporting are next. Provider controls are available
locally through `gpu-ctl` (see [provider controls](provider-controls.md#local-provider-cli)).

## Completed

- Digest allowlist validation.
- Automatic lease acceptance and rejection.
- Rootless Podman pull, create, start, wait, and cleanup.
- Lease expiry, failure, cancellation, and cleanup handling.
- `Drain()` and `Reclaim()` in the lease manager with tests.
- Local single-offer execution through `cmd/node-agent`.
- Configuration loading (`internal/config`, `node-agent -config`).
- Coordinator registration built from capability discovery, gated so the node
  takes no offers before it registers, and a heartbeat goroutine in
  `node-agent -serve` (`internal/agent`, `internal/coordinator`).
- Secure local control path for `Drain()` and `Reclaim()` through
  `node-agent -serve` and `gpu-ctl` over a private Unix socket.
- P16 coordinator client and `coordinator.LeaseReporter` adapter, with tests.
- Heartbeat loss reclaims active work and recovery reopens lease admission.
- `gpu-ctl status` shows the latest coordinator status. Removal stops
  heartbeats and lease admission while the local control socket stays open.
- GitHub Actions CI for Go tests, focused race tests, `go vet`, and web lint/build
  checks. See [continuous integration](ci.md).
- Live dashboard attached to `node-agent -serve` via `-dashboard` (loopback),
  with adapters from the lease manager, capability probes, and a verification
  guide. See [live dashboard testing](dashboard-live-testing.md).

## Next tasks

1. Agree the `runtimes` and `tier` vocabularies with Project 7b (R5, R8).
2. Poll lease offers (L1) once registered and feed them to the lease manager
   once the coordinator's `job_requirements` format is agreed.
3. Use `coordinator.LeaseReporter` for remote leases (L3–L7).
4. Add end-to-end and failure tests for coordinator, heartbeat, and control
   behavior.

## Linux validation

When a Linux host is available, run
[`scripts/smoke-podman.sh`](../scripts/smoke-podman.sh) and verify rootless
Podman, cgroups, namespaces, networking, filesystem restrictions, and cleanup.
The current tests use fakes and cannot validate those host-level properties.

## Later operational work

- Durable result retries.
- Crash recovery and orphan-container cleanup.
- Image-cache pruning.
- Control-action audit logging.
- Coordinator authentication and authorization.

