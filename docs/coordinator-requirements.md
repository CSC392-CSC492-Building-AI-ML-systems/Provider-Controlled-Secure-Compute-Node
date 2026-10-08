# Node ↔ Coordinator Requirements (Heartbeat, Lease, Registration, Drain)

Requirements the node must satisfy to interoperate with Project 16's coordinator
([Federated-Compute-Coordinator-Scheduling](https://github.com/CSC392-CSC492-Building-AI-ML-systems/Federated-Compute-Coordinator-Scheduling),
read at `main` @ `5d4461c`, Oct 8 2026).

**P16 is not the source of truth.** Their README says names and signatures are
"not a finalized API contract", and their code cites a `CSC398_API_CONTRACT`
document we have not seen. We start compatible with them and keep the
coordinator client behind one Go package so the wire format can change in one place.

Source tags:

- **[impl]**: implemented and running on P16 `main`.
- **[harness]**: only in P16's `harness/client.py` (their fake-provider client). Endpoint is planned, server does not serve it yet.
- **[ours]**: from our proposal / mentor diagram, not from P16.
- **[open]**: undecided; we pick a configurable default.

## 1. Transport

| ID | Requirement | Source |
| --- | --- | --- |
| T1 | JSON over HTTP. Base URL is configurable (Tailscale/Headscale address in prod, localhost for the mock). | impl |
| T2 | Errors arrive as `{"error": {"code": str, "message": str}}` with a non-2xx status. Parse `code`; fall back to `UNKNOWN` + raw body if malformed. | impl |
| T3 | Send `X-API-Key` header when a key is configured. P16 does not check it yet. | harness |
| T4 | Request bodies must contain **only** the documented fields. P16 uses `extra="forbid"`, so any extra field gives `422`. | impl |
| T5 | Timestamps are ISO 8601 UTC. The coordinator's clock is authoritative; never use node time for deadlines. | impl |
| T6 | Request timeout ~5 s (matches their client). A timed-out call counts as a failed attempt, not a crash. | harness |
| T7 | Validate every coordinator response before acting on it (proposal §7.2: "node only accepts correct input"). | ours |

## 2. Registration

`POST /providers` [impl]

```json
{
  "provider_id": "dh2020pc08",
  "capabilities": {
    "gpu_model": "NVIDIA GeForce RTX 4080",
    "vram_mb": 16376,
    "driver_version": "580.178.04",
    "runtimes": ["..."]
  },
  "accepted_tiers": ["..."]
}
```

| ID | Requirement | Source |
| --- | --- | --- |
| R1 | Register once on startup, before heartbeating or polling. | impl |
| R2 | `provider_id` non-empty and stable across restarts (from config, not random). | impl |
| R3 | `gpu_model` non-empty, `vram_mb > 0`. A node with no GPU cannot register as-is; decide whether to refuse to start or report CPU-only (P16 has no CPU-only path). | impl / open |
| R4 | Only the 4 capability fields above. Our richer capability report (UUID, PCI bus, compute cap, CDI devices, host info) must be mapped down, not sent raw (T4). | impl |
| R5 | `accepted_tiers` comes from provider config. Jobs whose `tier` is not listed are never offered. | impl |
| R6 | `201` → registered, response has `provider_id, status, registered_at, last_heartbeat_at`. Registration counts as the first heartbeat. | impl |
| R7 | `409 DUPLICATE_ID` → already registered (e.g. node restarted). Treat as success and continue heartbeating; no re-register/update endpoint exists. Capabilities changes are not propagated. | impl / open |
| R8 | `runtimes` vocabulary is free-form strings matched exactly against job `required_runtime`. Agree the vocabulary with P16 (e.g. `"ollama"`, `"llama.cpp"`, `"podman"`). | impl / open |

## 3. Heartbeat

`POST /providers/{provider_id}/heartbeat` [impl]

| ID | Requirement | Source |
| --- | --- | --- |
| H1 | Body is optional; if sent, only `{"provider_time": "<ISO 8601>"}` (logged by P16, never used for timing). | impl |
| H2 | Coordinator marks the provider stale after **15 s** with no heartbeat (`heartbeat_timeout_seconds`, a P16 proposal). Interval must be configurable, default **5 s** (survives 2 missed beats). | impl / open |
| H3 | Heartbeats run independently of job execution. A long-running container or slow Podman call must not delay them (separate goroutine). | ours |
| H4 | Response `200 {provider_id, status, server_received_at}`. `status` ∈ `ACTIVE, STALE, DRAINING, DRAINED`; the node must track the coordinator's view of itself from this. | impl |
| H5 | `404 NOT_FOUND` → coordinator lost us (e.g. it restarted, its store is in-memory). Re-register (R1), then resume. | impl / ours |
| H6 | `409 INVALID_STATE_TRANSITION` → we are `DRAINED`. Stop heartbeating and polling; node is out of the federation until re-registered. | impl |
| H7 | Network errors: keep retrying at the normal interval (no exponential backoff beyond the timeout window), log each failure. | ours |
| H8 | **Heartbeats do not extend leases.** Only renew (L6) does. | harness |

### What the coordinator does on a missed heartbeat [impl]

After the timeout: `ACTIVE → STALE`, `DRAINING → DRAINED`. All our `OFFERED`/`ACTIVE`
leases become `REVOKED` (reason `PROVIDER_STALE`) and their jobs are requeued.
A later heartbeat moves `STALE → ACTIVE`, but **revoked leases stay revoked**.

| ID | Requirement | Source |
| --- | --- | --- |
| H9 | If a lease call returns a revoked/not-found lease, stop that lease's container and clean up. Running work is no longer ours. Note: a heartbeat response never shows `STALE`, because P16 flips `STALE → ACTIVE` before responding, so the node cannot learn about revocation from heartbeats. | impl / ours |
| H10 | Locally: if heartbeats fail for longer than the timeout, assume our leases are revoked and stop the containers (lost-heartbeat failure case). | ours |

## 4. Lease lifecycle

Pull model: the node polls; the coordinator never pushes.

```
OFFERED ──accept──► ACTIVE ──started──► (running) ──renew…──► report ──► RELEASED
   │                  │
   └──reject──► REJECTED   expiry / stale / drain ──► EXPIRED / REVOKED
```

Lease statuses: `OFFERED, ACTIVE, REJECTED, EXPIRED, REVOKED, RELEASED`
(terminal: `REJECTED, EXPIRED, REVOKED, RELEASED`). [impl, models only]

| ID | Requirement | Source |
| --- | --- | --- |
| L1 | Poll `GET /providers/{id}/lease-offers` → `{"leases": [...]}`, each with `job_requirements`. Poll interval configurable, default = heartbeat interval. | harness |
| L2 | Every offer passes the Security & Policy Gateway (allowlist, digest pin, provider accept/reject policy, local capacity) before accept. | ours |
| L3 | Accept: `POST /leases/{lease_id}/accept` `{provider_id}` → `lease_status, expires_at, job_status, attempt_count`. | harness |
| L4 | Reject: `POST /leases/{lease_id}/reject` `{provider_id, reason}`, `reason` ∈ `LOCAL_BUSY, TEMP_UNAVAILABLE, POLICY_REJECT, CAPABILITY_MISMATCH, RESOURCE_UNAVAILABLE`. Costs the job no attempt; that job is never re-offered to us. Map gateway outcomes: not allowlisted/bad digest → `POLICY_REJECT`; already running a job → `LOCAL_BUSY`; draining/paused → `TEMP_UNAVAILABLE`; not enough VRAM → `RESOURCE_UNAVAILABLE`. | harness / ours |
| L5 | Started: `POST /leases/{lease_id}/started` `{provider_id}` once the container is actually running → job becomes `RUNNING`. | harness |
| L6 | Renew: `POST /leases/{lease_id}/renew` `{provider_id, extend_seconds}` (`> 0`) before `expires_at`; renew at a configurable fraction of remaining time (default ½). New `expires_at` is capped at our `drain_deadline`. | harness |
| L7 | Report: `POST /leases/{lease_id}/report` `{provider_id, outcome, reason?}`, `outcome` ∈ `SUCCESS, FAILURE`, `reason` ∈ `EXECUTION_ERROR, RUNTIME_ERROR, CAPABILITY_MISMATCH, DISK_FULL, ARTIFACT_UPLOAD_FAILED`. Map: non-zero container exit → `EXECUTION_ERROR`; Podman/GPU failure → `RUNTIME_ERROR`. | harness |
| L8 | Always clean up the container, volumes, and temp files after report, on success, failure, revoke, and expiry. | ours (proposal §7.2) |
| L9 | One job at a time for the MVP (P16 scheduler gives a provider at most one new lease per tick). Reject extra offers with `LOCAL_BUSY`. | impl / ours |
| L10 | If `expires_at` passes without a successful renew, stop the container and treat the lease as lost. | ours |
| L11 | Lease state is persisted locally (e.g. a JSON file in the runtime dir) so a restarted node can find orphaned containers, stop them, and report `FAILURE` (proposal §7.4 crash-recovery demo). | ours |
| L12 | Every lease call sends our `provider_id`; treat `4xx` on a lease call as "this lease is no longer ours" (H9). | harness / ours |

## 5. Drain & reclaim

| ID | Requirement | Source |
| --- | --- | --- |
| D1 | `POST /providers/{id}/drain` `{grace_period_seconds}` (`> 0`) → `provider_id, status, drain_deadline`. Repeating does not extend the deadline. Triggered by `gpu-ctl drain`. | harness |
| D2 | While draining: reject all new offers (`TEMP_UNAVAILABLE`), keep heartbeating and renewing the running lease until it finishes or the deadline hits. | impl / ours |
| D3 | Coordinator moves `DRAINING → DRAINED` when we have no live lease or the deadline passes; heartbeats then get `409` (H6). | impl |
| D4 | Reclaim (`gpu-ctl reclaim`): immediately stop the running container, report `FAILURE` (`RUNTIME_ERROR` until P16 adds a reclaim reason), then drain. P16 has no reclaim endpoint; `DRAIN_RECLAIM` exists only as an internal reason. | ours / open |
| D5 | Node-side states (mentor diagram) map to P16 states: `IDLE`/`BUSY` ↔ `ACTIVE`, `DRAIN` ↔ `DRAINING`, `RECLAIM` → `DRAINING` then `DRAINED`. | ours |

## 6. Configuration defaults (all overridable)

| Setting | Default | Why |
| --- | --- | --- |
| `coordinator_url` | `http://127.0.0.1:8000` | P16 runs on uvicorn's default port |
| `provider_id` | hostname | stable (R2) |
| `accepted_tiers` | `[]`, must be set | R5 |
| `heartbeat_interval` | 5 s | ⅓ of P16's 15 s timeout |
| `poll_interval` | 5 s | L1 |
| `lease_renew_fraction` | 0.5 | L6 |
| `lease_extend_seconds` | 60 | open: P16 has not set lease durations |
| `drain_grace_period` | 300 s | open |
| `request_timeout` | 5 s | T6 |

## 7. Open questions for P16

1. Where is the `CSC398_API_CONTRACT` document?
2. Default lease duration after accept, and offer timeout (currently a 1 h placeholder)?
3. What is inside `job_requirements`? We need at least the container image + SHA-256 digest for the allowlist (L2).
4. Two small schema additions (see §8): `vram_free_mb` in the heartbeat body, and optionally `cuda_version` + `compute_capability` in `capabilities`. Also: a re-register/update endpoint for changed capabilities (R7)?
5. Agreed `runtimes` and `tier` vocabularies (R5, R8)?
6. How are results/output returned? `report` only carries success/failure.
7. A reclaim endpoint or `PROVIDER_RECLAIM` failure reason (D4)?
8. Auth: will `X-API-Key` be enforced, and how are keys issued?
9. Is `UNHEALTHY` + `POST /providers/{id}/recover` going into v1?

## 8. Capability fields beyond P16's schema

Capability discovery produces a richer report than P16 accepts. The coordinator
adapter maps it down to the 4 fields (R4); the rest stays on the node. Most
extra fields are for the node's own Security & Policy Gateway, which has the
final say on every offer anyway.

| Field | Who needs it | Use |
| --- | --- | --- |
| `cuda_version`, `compute_capability` | Coordinator (nice to have) | Images built for a newer CUDA fail on older drivers; some kernels need a minimum arch (FlashAttention ≥ 8.0, FP8 ≥ 8.9). Without them the coordinator offers, we reject `CAPABILITY_MISMATCH`, and that job is never re-offered to us. |
| `vram_free_mb` | Coordinator (**worth asking for**) | Actual availability; a provider using their own GPU (the lab PC already had 1.4 GB in use) may not fit a job sized against total VRAM. Changes over time, so it belongs in the heartbeat, not registration. |
| `gpus[]` (multi-GPU) | Coordinator (later) | P16 has one `gpu_model`/`vram_mb`; multi-GPU nodes cannot describe themselves. Lab machines are single-GPU. |
| `arch` | Coordinator (later) | `amd64` images do not run on `arm64`. Lab is all x86. |
| GPU UUID, PCI bus ID, CDI device names | Node only | Choosing which device goes into the container. |
| Podman installed/version/rootless, CDI available | Node only | Preconditions checked before registering. The coordinator cannot verify them, so reporting adds nothing. |
| Host CPU, RAM, OS, kernel | Node only | `gpu-ctl status`, logs. RAM only matters for scheduling with llama.cpp CPU offload (out of MVP scope). |
| `schema_version`, `collected_at`, `probe_errors` | Node only | Local report versioning and debugging. |
