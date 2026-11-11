# Provider controls

This document describes the provider-side controls currently implemented in this
repository. They are enforced by the in-process lease manager and container
engine. The repository does not currently implement a network coordinator
connection or an external command path to the running manager.

## Control summary

| Control | What it does | Current implementation status |
| --- | --- | --- |
| Allowlist | Checks exact image digests against configured repositories | Implemented and enforced before Podman is invoked |
| Accept | Reserves the node for a valid lease and reports acceptance | Implemented automatically by `lease.Manager.ReceiveOffer` |
| Reject | Declines invalid, expired, unapproved, active, or draining offers | Implemented automatically by `lease.Manager.ReceiveOffer` |
| Drain | Stops new offers while allowing the current workload to finish | Implemented in `lease.Manager`; not wired to `gpu-ctl` or the real coordinator |
| Reclaim | Immediately removes the current workload and reports reclamation | Implemented in `lease.Manager`; not wired to `gpu-ctl` or the real coordinator |

The current local command and its limitations are documented in the
[Podman execution guide](podman-execution.md). The coordinator client,
registration, and heartbeat transport are placeholders.

## Allowlist control

The node loads a JSON allowlist mapping an approved SHA-256 image digest to an
explicit registry repository:

```json
{
  "images": [
    {
      "digest": "sha256:<64 lowercase hexadecimal characters>",
      "image": "quay.io/example/inference"
    }
  ]
}
```

An offer carries the lease ID, image digest, and expiry. The lease manager checks
the digest against the loaded allowlist. If it is approved, the container engine
constructs the pinned reference `repository@digest`; if not, the offer is
rejected and Podman is not invoked. This is implemented by
[`ReceiveOffer`](../internal/lease/manager.go) and the
[allowlist checker](../internal/allowlist/checker.go).

The digest identifies one exact image manifest rather than a mutable tag such as
`latest`. The [OCI image manifest specification](https://github.com/opencontainers/image-spec/blob/main/manifest.md)
describes the image manifest and its content digests. Podman's image reference
and container lifecycle options are documented in the official
[Podman run documentation](https://docs.podman.io/en/latest/markdown/podman-run.1.html).

## Accept and reject

Acceptance is not currently a separate manual command. When the node receives an
offer, it:

1. Rejects it immediately if the node is draining or already has an active lease.
2. Validates the lease ID, digest, and expiry.
3. Checks the digest against the provider allowlist.
4. Atomically reserves the node.
5. Calls `Coordinator.ReportAccepted`; the local `node-agent` implementation
   writes an `accepted` JSON event.
6. Pulls and launches the approved image locally through rootless Podman.

Offers can be rejected with these reasons defined in
[`internal/lease/types.go`](../internal/lease/types.go):

- `invalid_offer` — required lease data is missing.
- `expired` — the lease expiry is not in the future.
- `digest_not_allowed` — the provider has not approved the image digest.
- `already_active` — another lease already owns the node.
- `draining` — the provider has stopped accepting new work.

The local `node-agent` emits JSON events for this boundary, but those events are
not yet network acknowledgements from the real coordinator. See
[`cmd/node-agent/main.go`](../cmd/node-agent/main.go) and the
[local execution format](podman-execution.md#local-single-offer-command).

## Drain

Drain is a graceful provider shutdown of new work:

```text
running job → drain requested → current job finishes → idle
```

Once draining begins:

- the manager state becomes `draining`;
- new offers are rejected with `draining`;
- the active lease and container are preserved;
- normal completion, failure, or expiry reporting still occurs;
- cleanup returns the manager to `idle`.

The implementation is [`Manager.Drain`](../internal/lease/manager.go). It is
currently callable only by code holding the in-process manager;
`cmd/gpu-ctl/main.go` still contains a placeholder. The `cmd/dashboard-mock`
command uses a mock controller for its dashboard endpoints rather than this
manager. The dashboard endpoint contract is described in
[`docs/dashboard-api.md`](dashboard-api.md).

## Reclaim

Reclaim is an emergency provider action:

```text
running job → reclaim requested → container removed → result=reclaimed → idle
```

The manager:

- takes ownership of the active workload operation so a concurrent wait cannot
  also manage it;
- force-removes the workload with a bounded cleanup context;
- reports `OutcomeReclaimed` with no exit code;
- releases the lease and returns to `idle`.

If container removal fails, the manager keeps the lease reserved and returns the
cleanup error. This prevents the node from accepting another job while the old
container may still exist. The implementation is
[`Manager.Reclaim`](../internal/lease/manager.go), with tests in
[`internal/lease/manager_test.go`](../internal/lease/manager_test.go).

## Podman policy in the current engine

After acceptance, the current container engine passes fixed security and
resource flags to local rootless Podman. It drops capabilities, disables
networking, uses a read-only root filesystem, runs as an unprivileged UID/GID,
and applies CPU, memory, PID, temporary-storage, and file-descriptor limits.
The full policy and cleanup behavior are in the
[Podman execution guide](podman-execution.md#container-policy-and-cleanup).

These flags are not supplied by a coordinator in the current code. The allowlist
remains necessary because containers share the host kernel; this is a curated
workload boundary, not a virtual-machine boundary.

## What is not wired yet

The following integration work remains outside this document's implementation:

- `gpu-ctl` commands that reach the running node agent;
- dashboard controls connected to the real lease manager;
- coordinator requests for drain and reclaim;
- registration, heartbeat, and durable result reporting;
- crash recovery and retry of uncertain container cleanup.

