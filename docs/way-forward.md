# Way forward

The provider-side lease and Podman path is implemented and tested with fakes.
Registration and heartbeats reach the coordinator; lease offers and reports do
not yet. Provider controls are available
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
- P16 coordinator client and a `lease.Coordinator` adapter for it, with tests.
- Registration and heartbeats with `node-agent -serve -coordinator`; new work
  stops while the coordinator is lost and resumes when it returns.

## Next tasks

1. Poll coordinator lease offers (`GET /providers/{id}/lease-offers`), turn
   them into `lease.Offer`s once P16 says what `job_requirements` holds, and
   report through `coordinator.LeaseReporter` instead of the local JSON
   reporter.
2. Stop the running container when the coordinator is lost (H10 in
   [coordinator requirements](coordinator-requirements.md)); today only new
   work stops.
3. Implement configuration loading in
   [`internal/config/config.go`](../internal/config/config.go); the
   coordinator connection is configured with `node-agent` flags for now.
4. Feed capability discovery into registration instead of `-gpu-model`,
   `-vram-mb` and `-driver` flags.
5. Connect the dashboard to the real manager instead of its mock controller.
6. Add CI for `go test`, `go test -race`, and `go vet`.

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

