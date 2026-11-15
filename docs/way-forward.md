# Way forward

The provider-side lease and Podman path is implemented and tested with fakes.
Coordinator networking is not wired yet. Provider controls are available
locally through `gpu-ctl` (see [provider controls](provider-controls.md#local-provider-cli)).

## Completed

- Digest allowlist validation.
- Automatic lease acceptance and rejection.
- Rootless Podman pull, create, start, wait, and cleanup.
- Lease expiry, failure, cancellation, and cleanup handling.
- `Drain()` and `Reclaim()` in the lease manager with tests.
- Local single-offer execution through `cmd/node-agent`.
- Secure local control path for `Drain()` and `Reclaim()` through
  `node-agent -serve` and `gpu-ctl` over a private Unix socket.

## Next tasks

1. Define the coordinator protocol for registration, heartbeats, lease offers,
   results, and connectivity failures.
2. Implement configuration loading in
   [`internal/config/config.go`](../internal/config/config.go).
3. Implement the coordinator client in
   [`internal/coordinator/client.go`](../internal/coordinator/client.go).
4. Add registration, heartbeats, and coordinator connectivity handling.
5. Connect the dashboard to the real manager instead of its mock controller.
6. Add end-to-end and failure tests for coordinator, heartbeat, and control
   behavior.
7. Add CI for `go test`, `go test -race`, and `go vet`.

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

