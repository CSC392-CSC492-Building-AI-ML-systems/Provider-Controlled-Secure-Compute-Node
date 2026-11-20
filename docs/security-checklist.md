# Security checklist

Protections the provider node is expected to enforce for curated inference
workloads. Use this as a pre-demo and pre-deploy review. Details live in the
linked guides.

## Rootless containers

- [ ] Node agent runs as a normal user (not root / not via sudo).
- [ ] `podman info` reports rootless `true` for that user.
- [ ] Subordinate UID/GID ranges exist in `/etc/subuid` and `/etc/subgid`.
- [ ] Workloads use `--userns=nomap` and UID/GID `65534:65534`.

See [Podman execution](podman-execution.md#host-prerequisites).

## Image pinning and allowlist

- [ ] Provider allowlist is a local JSON file writable only by the provider.
- [ ] Every entry uses `sha256:` + 64 lowercase hex (exact manifest digest).
- [ ] Repository names include an explicit registry (no short names / tags).
- [ ] Offers with an unknown digest are rejected before Podman is invoked.

See [Allowlist](allowlist.md) and [examples/allowlist.json](../examples/allowlist.json).

## Host isolation

- [ ] Container networking is disabled (no ports, no host LAN access from the job).
- [ ] No host bind mounts except the engine-managed temporary workspace.
- [ ] Root filesystem is read-only; `/tmp` is a bounded `noexec` tmpfs.
- [ ] All Linux capabilities are dropped; no-new-privileges remains in effect.
- [ ] PID / IPC / UTS namespaces stay private to the container.

See [Podman execution — container policy](podman-execution.md#container-policy-and-cleanup).

## Resource limits

- [ ] CPU, memory, PID, and open-file caps are applied by the engine (defaults:
      1 CPU, 512 MiB, 128 processes, 1024 FDs).
- [ ] Lease expiry and Conmon lifetime caps bound runaway jobs.
- [ ] Provider can reclaim immediately and drain to refuse new work.

See [Provider controls](provider-controls.md).

## Local control plane

- [ ] Dashboard (if enabled) listens only on loopback (`127.0.0.1` / `::1`).
- [ ] `gpu-ctl` talks over a Unix socket with directory mode `0700` and socket
      mode `0600`, owned by the agent user.
- [ ] Socket path is never exposed on the network.

See [Provider controls — local CLI](provider-controls.md#local-provider-cli) and
[Dashboard API](dashboard-api.md).

## Coordinator boundary (when wired)

- [ ] Registration and heartbeats use the agreed coordinator base URL (private
      mesh in production).
- [ ] Lease digests still pass the local allowlist even if the coordinator is
      compromised or misconfigured.
- [ ] Lost-heartbeat / reclaim paths stop containers and free the node.

See [Coordinator requirements](coordinator-requirements.md) and
[Failure cases](failure-cases.md).

## Verification commands

```sh
go test ./...
go test -race ./internal/containerengine ./internal/lease ./cmd/node-agent
go vet ./...
./scripts/smoke-podman.sh   # Linux rootless host
```
