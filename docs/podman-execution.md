# Running accepted jobs with Podman

The lender validates an offer against its provider-owned allowlist, reserves the
node, and reports acceptance **before** downloading anything. The engine resolves
the digest to the repository in that allowlist, pulls `repository@sha256:…` with
TLS verification, creates a container, and starts the image's default entrypoint.
No shell command or runtime flags come from the coordinator.

`lease.NewManager(checker, containerengine.New(checker), coordinator)` wires this
path into a coordinator adapter. Call `ReceiveOffer(ctx, offer)`, then `Wait(ctx)`
after successful launch to collect the result and release the node. The network
coordinator client, registration, and heartbeat transport remain placeholders;
this change supplies a local command for exercising the same lease boundary.

## Host prerequisites

Use a Linux host with Podman on `PATH`, a regular service account with subordinate
UID/GID ranges configured in `/etc/subuid` and `/etc/subgid`, and cgroup v2 with
CPU, memory and PID controllers delegated to that account. Do not run the agent
with sudo. Verify rootless setup as the service account:

```sh
podman --remote=false info --format '{{.Host.Security.Rootless}}'
```

This must print `true`; unavailable or rootful Podman fails closed. Resource
limits must be supported by the host. Podman command failures are reported rather
than retried with weaker flags. The provider controls Podman's configuration,
registry credentials and executable. No daemon or Podman API socket is required.

## Local single-offer command

Build with Go 1.23.4 or newer:

```sh
go build -o /tmp/node-agent ./cmd/node-agent
/tmp/node-agent -allowlist /path/to/allowlist.json < /path/to/offer.json
```

An allowlist file has this form (replace the placeholder with the actual approved
64-character lowercase SHA-256 digest):

```json
{
  "images": [
    {"digest": "sha256:<64 hex characters>", "image": "registry.example/team/job"}
  ]
}
```

The repository must include an explicit registry, such as `docker.io/library/job`
or `registry.example:5000/team/job`. Tags, short image names and alternate image
transports are rejected. Keep this file writable only by the provider.

Supply exactly one offer on stdin; use a future RFC3339 expiry:

```json
{
  "ID": "lease-123",
  "ImageDigest": "sha256:<same approved digest>",
  "ExpiresAt": "2026-12-01T12:00:00Z"
}
```

Stdout contains newline-delimited JSON events, for example:

```json
{"lease_id":"lease-123","type":"accepted"}
{"exit_code":0,"lease_id":"lease-123","outcome":"completed","type":"result"}
```

These are local events, not network acknowledgements from the remote coordinator.
Rejected offers never invoke Podman. A launch failure emits a failed result and
returns a command error. A nonzero job exit produces a failed result with its exit
code; the command itself succeeds if execution supervision and reporting succeed.
Diagnostics go to stderr. Workload logs and output files are not collected.

## Container policy and cleanup

- Local rootless Podman only, with `--userns=nomap` and UID/GID `65534:65534`.
- All Linux capabilities dropped; no privilege escalation; Podman's default
  seccomp and SELinux/AppArmor protections remain enabled.
- No networking, exposed ports, host mounts, extra devices or GPU passthrough.
  PID, IPC and UTS namespaces are private; HTTP proxy environment injection is off.
- Read-only root filesystem; image-declared volumes are ignored. The only
  application scratch directory is a 64 MiB `/tmp` tmpfs with `noexec,nosuid,nodev`.
- One CPU, 512 MiB memory, no additional swap allowance, 128 processes and 1024
  open files. These conservative fixed limits suit small curated jobs; images
  must support the non-root user, read-only filesystem and offline operation.
- Image health checks, restart policy and persistent workload logging are disabled.

Setup has a five-minute timeout, bounded further by the offer's expiry. The
manager supervises execution until expiry or context cancellation (including
SIGINT/SIGTERM in the command). Conmon also enforces a maximum runtime based on
the remaining lease, capped at one hour, with whole-second granularity. This
provides a fallback kill timer if the agent exits unexpectedly.

Containers have random node-owned names. They are retained until exit status is
collected, then force-removed. Failed creation/start and cancelled waits also
attempt removal with an independent ten-second timeout. Cleanup failure keeps
the manager reserved and preserves the workload handle for retry. Successful
cleanup releases the node even if result reporting fails; durable report retries,
crash recovery, image-cache pruning, drain/reclaim controls and output collection
are not implemented here. After an unclean agent exit, stopped containers may
require provider cleanup (`podman ps -a --filter name=compute-node-`).

The allowlist remains essential: containers share the host kernel. This policy
supports curated workloads, not arbitrary untrusted code or a VM boundary.

Podman option semantics: [run](https://docs.podman.io/en/latest/markdown/podman-run.1.html),
[global/local mode](https://docs.podman.io/en/latest/markdown/podman.1.html).

## Verification

```sh
go test ./...
go test -race ./internal/containerengine ./internal/lease ./cmd/node-agent
go vet ./...
```

Tests use a command recorder and an executable Podman substitute to exercise
acceptance-before-pull, policy flags, rejected offers, launch failures, nonzero
exits, cancellation cleanup and result reporting. They do not establish that a
host's kernel or cgroup delegation enforces the limits. Validate an approved,
small image on the intended rootless Podman host before deployment.
