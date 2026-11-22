# Failure cases

How the node should behave when something goes wrong. Local lease and Podman
paths are implemented today, including coordinator registration and heartbeat
handling (see [way-forward](way-forward.md) and
[coordinator requirements](coordinator-requirements.md)).

## Container crash or nonzero exit

**Symptom:** The curated job process exits with a nonzero code, or Podman
reports that the container stopped unexpectedly.

**Expected behavior:**

1. The lease manager collects the exit status (and, for inference images,
   validates `response.json` when present).
2. It reports `outcome=failed` with the exit code through the local reporter
   (and later through the coordinator client).
3. The container is force-removed; on successful cleanup the node returns to
   `idle` and can accept another offer.
4. If cleanup fails, the manager stays reserved so a second job cannot start
   while an orphan may still exist. The provider inspects with
   `podman ps -a --filter name=compute-node-` and reclaims or removes manually.

**How to exercise locally:** run an allowlisted image whose entrypoint exits
non-zero via `node-agent` stdin offer or `gpu-ctl run`, then check the JSON
`result` event and `gpu-ctl status`.

## Launch failure (pull / create / start)

**Symptom:** Digest is allowlisted but Podman cannot pull, create, or start.

**Expected behavior:** Acceptance may already have been reported; the manager
emits a failed result, attempts cleanup, and surfaces a command error for the
local single-offer path. The node must not remain forever in a half-launched
state without a result event.

## Lost heartbeat / coordinator unreachable

**Symptom:** Heartbeats fail longer than the coordinator stale timeout (P16
default 15s), or the coordinator returns `404` (forgotten provider) /
`409` after drain.

**Expected behavior:**

1. The heartbeat loop logs failures and retries at the configured interval.
2. After `lost_after`, the node refuses new leases and cancels the current
   launch or wait so the workload can be removed. If cleanup fails, the lease
   stays reserved for manual reclaim.
3. On `404 NOT_FOUND`, the node immediately revokes its current lease and
   attempts to re-register. Successful re-registration or a successful
   heartbeat after a network outage restores lease admission once the prior
   workload has been cleared.
4. On `409`, the node shows `DRAINED` in `gpu-ctl status`, reclaims any active
   lease, and stops heartbeating and taking leases. The local control socket
   stays open. Restart the node agent to attempt registration again.

The coordinator field in `gpu-ctl status` shows the last reported status.

## Emergency reclaim

**Symptom:** Provider needs the GPU back immediately (security concern, local
workload, thermal, or demo interrupt).

**Expected behavior:**

1. `gpu-ctl reclaim` or dashboard **Reclaim** takes ownership of the active
   wait, force-removes the container, and reports `outcome=reclaimed`.
2. The node returns to `idle` only after cleanup succeeds.
3. A failed reclaim leaves the lease reserved so another offer cannot start.

See [Provider controls — reclaim](provider-controls.md#reclaim).

## Drain then idle

**Symptom:** Provider wants no new jobs but will finish the current one.

**Expected behavior:**

1. `gpu-ctl drain` (or dashboard **Drain**) sets state `draining`.
2. New offers are rejected with `draining`.
3. When the current job completes, fails, expires, or is reclaimed, the node
   becomes `idle` while still draining until `gpu-ctl resume`.

## Agent crash / SIGTERM

**Symptom:** `node-agent` is killed while a container runs.

**Expected behavior:**

1. Context cancellation cancels the active wait and attempts container removal.
2. After an unclean exit, stopped containers named `compute-node-*` may remain;
   the provider removes them before starting a new agent on the same host.
3. Durable crash recovery and orphan sweeping are listed as later operational
   work in [way-forward](way-forward.md).

## Coordinator reports / connectivity failures

**Symptom:** Local execution succeeds but `ReportAccepted` / `ReportResult`
fails (future network client).

**Expected behavior (target):** keep retrying durable reports without losing
local cleanup success; do not accept a second lease while reporting for the
first is still uncertain. Today the local reporter writes JSON lines to stdout
only—there is no network retry loop yet.
