# Way forward

The provider-side lease and Podman path is implemented and tested with fakes.
Registration and heartbeats are wired; acting on heartbeat results, lease
offer polling, and coordinator result reporting are not. Provider controls are available
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

## Next tasks

1. Heartbeat results (Guneev, extending `internal/agent/heartbeat.go`):
   - Add an `OnRecovered` callback, so the node accepts leases again after an
     outage. Wire `OnLost` and `OnRecovered` in `cmd/node-agent/federation.go`
     to `SetCoordinatorConnected(false/true)`, and reclaim the running job on
     lost (H10).
   - Track the coordinator's view of the node (`ACTIVE`, `DRAINING`,
     `DRAINED`) from each heartbeat reply (H4). Show it in `gpu-ctl status`.
   - On removal (`ErrRemoved`, H6), stop heartbeating and refuse leases, but
     keep the local control socket up. Today serve exits instead.
   - Add tests for recovery and status. Update the lost-heartbeat section of
     [failure cases](failure-cases.md).
2. Agree the `runtimes` and `tier` vocabularies with Project 7b (R5, R8).
3. Poll lease offers (L1) once registered and feed them to the lease manager.
4. Replace the local reporter with coordinator lease calls (L3–L7).
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

