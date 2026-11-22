# Provider controls

This document describes the provider-side controls currently implemented in this
repository. They are enforced by the in-process lease manager and container
engine. The provider reaches a running node agent through `gpu-ctl` over a
local Unix socket (see [Local provider CLI](#local-provider-cli)).

## Control summary

| Control | What it does | Current implementation status |
| --- | --- | --- |
| Allowlist | Checks exact image digests against configured repositories | Implemented and enforced before Podman is invoked |
| Accept | Reserves the node for a valid lease and reports acceptance | Implemented automatically by `lease.Manager.ReceiveOffer` |
| Reject | Declines invalid, expired, unapproved, active, or draining offers | Implemented automatically by `lease.Manager.ReceiveOffer` |
| Drain | Stops new offers while allowing the current workload to finish | Implemented in `lease.Manager`; available as `gpu-ctl drain` and `gpu-ctl resume`; not wired to the real coordinator |
| Reclaim | Immediately removes the current workload and reports reclamation | Implemented in `lease.Manager`; available as `gpu-ctl reclaim`; not wired to the real coordinator |
| Run | Starts an approved image chosen by the provider as a local lease | `gpu-ctl run`, for local testing and demos; the coordinator chooses jobs in the real flow |

The current local command and its limitations are documented in the
[Podman execution guide](podman-execution.md). A federated agent refuses new
work, including `gpu-ctl run`, when it loses the coordinator. Use
`gpu-ctl status` to see the coordinator's last reported status.

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
available locally as `gpu-ctl drain`, and `gpu-ctl resume` ends a drain on an
idle node so it accepts offers again. The `cmd/dashboard-mock`
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

## Local provider CLI

`node-agent -serve` keeps the lease manager running and listens for
commands on a Unix socket. `gpu-ctl` sends them:

```sh
node-agent -serve -allowlist /path/to/allowlist.json   # in one terminal
gpu-ctl images               # list approved images
gpu-ctl run                  # choose an image from a numbered list
gpu-ctl run -for 10m 2       # or pick by number, digest or repository
gpu-ctl status               # state, active job and last result
gpu-ctl drain                # stop accepting new jobs
gpu-ctl resume               # accept new jobs again
gpu-ctl reclaim              # stop the current job immediately
```

`gpu-ctl run` starts the chosen image as a local lease with a `local-` lease
ID, lasting 30 minutes by default and at most one hour, the container
engine's lifetime cap. Lease events are printed by `node-agent` as JSON lines,
the same format as the single-offer command. Stopping `node-agent` cancels
and removes a running job.

The socket defaults to `$XDG_RUNTIME_DIR/compute-node/control.sock`, or the
user's temporary directory when `XDG_RUNTIME_DIR` is unset; both commands
accept `-socket` to change it. Access is controlled by file permissions: the
agent refuses to start unless the socket's directory is a real directory
owned by the current user with mode `0700`, and the socket itself is `0600`.
Only that user can control the node, and the socket is never exposed on the
network. `gpu-ctl` must therefore run on the node as the same user as
`node-agent`.

Reclaim works while the agent is collecting a job's result: the lease manager
kills the workload, the waiting collector reports `reclaimed` instead of a
failure, and the node is released before `gpu-ctl reclaim` returns. See
[`internal/control`](../internal/control) and
[`cmd/gpu-ctl`](../cmd/gpu-ctl/main.go).

## What is not wired yet

The following integration work remains outside this document's implementation:

- dashboard controls connected to the real lease manager;
- coordinator requests for drain and reclaim;
- registration, heartbeat, and durable result reporting;
- crash recovery and retry of uncertain container cleanup.

