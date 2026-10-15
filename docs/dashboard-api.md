# Dashboard API contract

The node agent serves a small HTTP API that the dashboard (`web/`) reads from.
Both groups build against this document: the backend implements it, the UI
consumes it, and changes to it are agreed first, then made here.

Go types for everything below live in `internal/dashboard/types.go`. The mock
server (`go run ./cmd/dashboard-mock`) returns example data for every endpoint.

## Ground rules

- The server binds to `127.0.0.1` only. The dashboard can drain and reclaim the
  node, so exposing it on the LAN would break the host-isolation requirement.
  Remote access goes through the private Tailscale/Headscale network.
- All bodies are JSON. Times are RFC 3339 UTC strings.
- Memory is in MiB, in fields ending `_mb` (same convention as
  `docs/coordinator-requirements.md`).
- Errors return a 4xx/5xx status with `{"error": "<message>"}`.
- An endpoint whose subsystem is not wired in yet returns `503` with an error
  message, so the UI can show "not available" instead of breaking.

## Endpoints

| Method | Path | Owner | Purpose |
| --- | --- | --- | --- |
| GET | `/api/status` | heartbeat-lease | Agent state, lease, coordinator view, mesh IP |
| GET | `/api/capabilities` | capability | Static capability report |
| GET | `/api/metrics` | capability | Live per-GPU usage |
| GET | `/api/probes` | capability | Validation matrix rows with their last result |
| POST | `/api/probes/run` | capability | Run every probe, return all rows |
| POST | `/api/probes/{id}/run` | capability | Run one probe, return its row |
| GET | `/api/events` | heartbeat-lease | Server-Sent Events stream for the diagnostic log |
| POST | `/api/control/drain` | heartbeat-lease | Provider drain |
| POST | `/api/control/reclaim` | heartbeat-lease | Provider emergency reclaim |

Payload studio endpoints (capability register payload, lease dispatch,
tamper/reject test, local CLI socket) are TBD and come in step 2.

### GET /api/status

```json
{
  "provider_id": "dh2020pc08",
  "state": "BUSY",
  "lease_id": "lease-req-2026-9041",
  "coordinator_status": "ACTIVE",
  "last_heartbeat_at": "2026-10-14T23:40:05Z",
  "mesh_ip": "100.64.0.12"
}
```

- `state`: `IDLE | BUSY | DRAIN | RECLAIM` (node-side states, mapping in
  `docs/coordinator-requirements.md` D5).
- `coordinator_status`: `UNREGISTERED | ACTIVE | STALE | DRAINING | DRAINED`.
- `lease_id`, `last_heartbeat_at`, `mesh_ip` are omitted when unknown.

### GET /api/capabilities

```json
{
  "schema_version": "1",
  "collected_at": "2026-10-14T23:40:00Z",
  "gpus": [
    {
      "index": 0,
      "uuid": "GPU-8a1f2c3d-0000-1111-2222-333344445555",
      "name": "NVIDIA GeForce RTX 4080",
      "vram_total_mb": 16376,
      "pci_bus_id": "00000000:01:00.0",
      "compute_capability": { "major": 8, "minor": 9 },
      "mig_enabled": false
    }
  ],
  "driver": { "version": "550.54.14", "cuda_version": "12.4" },
  "runtimes": {
    "podman": { "installed": true, "version": "4.9.3", "rootless": true },
    "cdi": { "available": true, "devices": ["nvidia.com/gpu=0", "nvidia.com/gpu=all"] }
  },
  "host": {
    "cpu_model": "Intel(R) Core(TM) i7-12700",
    "cpu_cores": 20,
    "ram_total_mb": 31842,
    "os": "Ubuntu 22.04.4 LTS",
    "kernel": "6.5.0-35-generic",
    "arch": "amd64"
  },
  "probe_errors": []
}
```

- No GPU: `"gpus": []`, never `null`.
- `probe_errors`: `[{"probe": "podman", "error": "..."}]` for detection steps
  that failed; everything else is still reported.

### GET /api/metrics

```json
{
  "collected_at": "2026-10-14T23:40:05Z",
  "gpus": [
    {
      "uuid": "GPU-8a1f2c3d-0000-1111-2222-333344445555",
      "vram_used_mb": 1432,
      "vram_free_mb": 14944,
      "utilization_pct": 7,
      "temperature_c": 41
    }
  ]
}
```

GPUs are matched to `/api/capabilities` by `uuid`.

### Probes

A probe is one row of the validation matrix: a check that returns
`pass | warn | fail` plus a message. Two more statuses come from the server
itself: `not_implemented` (no probe registered for that id yet) and `not_run`
(registered, but not run since the agent started). A probe that panics or runs
longer than 10 s is reported as `fail`.

`GET /api/probes` and `POST /api/probes/run` return:

```json
{
  "probes": [
    {
      "id": "capability_discovery",
      "name": "Capability Discovery",
      "requirement": "Verify NVML parsing, driver version & CUDA memory reporting",
      "layer": "Provider Agent",
      "owner": "capability",
      "status": "pass",
      "message": "1 GPU detected, driver 550.54.14",
      "checked_at": "2026-10-14T23:40:00Z"
    }
  ]
}
```

`POST /api/probes/{id}/run` returns one row (the object inside `probes`), or
`404` for an unknown id. `checked_at` is omitted for probes that never ran.

The probe ids are fixed. A probe with no implementation registered reports
`not_implemented`, so every row shows in the UI from day one.

| id | Name | Requirement | Layer | Owner |
| --- | --- | --- | --- | --- |
| `overlay_network` | Encrypted Overlay Network | Verify WireGuard node registration & IP allocation | Tailscale/Headscale | networking |
| `capability_discovery` | Capability Discovery | Verify NVML parsing, driver version & CUDA memory reporting | Provider Agent | capability |
| `lease_heartbeat` | Lease & Heartbeat Manager | Validate transitions IDLE -> BUSY -> DRAIN -> RECLAIM | Host Daemon | heartbeat-lease |
| `policy_gateway` | Security & Policy Gateway | Validate container allowlist & pinned SHA-256 digest match | Host Daemon | container-security |
| `rootless_podman_cdi` | Rootless Podman + CDI | Verify unprivileged CDI device passthrough (nvidia.com/gpu=all) | Container Boundary | capability |
| `privilege_dropping` | Kernel Privilege Dropping | Enforce --cap-drop=ALL and --security-opt no-new-privileges | Container Security | container-security |
| `model_mount` | GGUF Model Storage Mount | Verify read-only bind mount on /var/cache/models:/models:ro | Filesystem Isolation | capability |
| `lan_isolation` | Host LAN Network Isolation | Block 192.168.0.0/16 & 10.0.0.0/8; permit overlay/loopback only | Network Policy | security-hardening |
| `ipc_socket` | Local IPC Provider Socket | Verify /tmp/gpu-agent.sock permissions are 0600 (owner only) | Local Ownership | heartbeat-lease |

### GET /api/events

Server-Sent Events. On connect the server sends the most recent events (up to
100), then streams new ones. Each message is one JSON event:

```text
data: {"time":"2026-10-14T23:40:05Z","level":"info","source":"heartbeat","message":"heartbeat ok"}
```

- `level`: `info | warn | error`.
- `source`: the subsystem that produced it, e.g. `heartbeat`, `lease`,
  `capability`, `probe`, `control`.

### POST /api/control/drain

Body (optional): `{"grace_period_seconds": 300}`. Default 300.
Returns `202` with the new `/api/status` body, `400` if the grace period is not
positive, or `409` if the node refuses (e.g. it is already draining).

### POST /api/control/reclaim

No body. Stops running work immediately, then drains. Returns `202` with the new
`/api/status` body, or `409` if the node refuses (e.g. already reclaimed).

## Ownership

| Group | Implements |
| --- | --- |
| capability | `CapabilitySource` (capabilities, metrics), probes `capability_discovery`, `rootless_podman_cdi`, `model_mount`; UI: VRAM, CDI, sandbox and model-mount cards, validation matrix, audit and export |
| heartbeat-lease | `StatusSource`, `Controller`, events from the agent; probes `lease_heartbeat`, `ipc_socket`; UI: agent-state card, provider controls, diagnostic stream |
| other milestones | Probes `overlay_network` (networking), `policy_gateway` and `privilege_dropping` (container-security), `lan_isolation` (security-hardening) |

The interfaces each group implements are in `internal/dashboard/sources.go`
and `internal/dashboard/probe.go`.
