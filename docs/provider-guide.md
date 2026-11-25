# Provider configuration guide

End-to-end setup for operating a provider-controlled secure compute node on a
single Linux machine. This umbrella links the detailed docs; follow them in
order for a first bring-up.

## 1. Host setup

1. Install rootless Podman and cgroup v2 delegation for your service account.
2. Confirm rootless mode and subordinate UID/GID maps.
3. (Optional) Install NVIDIA driver tools (`nvidia-smi`) and CDI
   (`nvidia-ctk`) if you will report GPU capabilities.

Details: [Podman execution — host prerequisites](podman-execution.md#host-prerequisites),
[GPU SMI](gpu-smi.md), [Capability design](design-choices.md).

## 2. Allowlist

1. Copy [examples/allowlist.json](../examples/allowlist.json).
2. Replace the placeholder digest and repository with your approved,
   digest-pinned inference image (see `containers/ollama/publish-image.sh`).
3. Restrict file permissions so only the provider user can write the file.

Details: [Allowlist](allowlist.md).

## 3. Run the node agent

Build:

```sh
go build -o /tmp/node-agent ./cmd/node-agent
go build -o /tmp/gpu-ctl ./cmd/gpu-ctl
```

**Single offer (stdin):**

```sh
/tmp/node-agent -allowlist /path/to/allowlist.json < /path/to/offer.json
```

**Interactive menu:**

```sh
/tmp/node-agent -interactive
```

**Long-running agent for local controls** (required for `gpu-ctl` and the live
dashboard):

```sh
/tmp/node-agent -serve -allowlist /path/to/allowlist.json
```

With the live dashboard (loopback only):

```sh
/tmp/node-agent -serve -allowlist /path/to/allowlist.json \
  -dashboard 127.0.0.1:8080 -web web/dist
```

Do not combine `-serve` with `-interactive`. `-dashboard` requires `-serve`
and refuses non-loopback addresses.

Details: [Podman execution](podman-execution.md),
[Provider controls](provider-controls.md),
[Live dashboard testing](dashboard-live-testing.md).

## 4. Provider controls

In a second terminal, as the same user:

```sh
/tmp/gpu-ctl images
/tmp/gpu-ctl status
/tmp/gpu-ctl run                 # or: gpu-ctl run -for 10m <n>
/tmp/gpu-ctl drain
/tmp/gpu-ctl resume
/tmp/gpu-ctl reclaim
```

`-for` must be a whole number of seconds between `1s` and `1h`.

Reclaim stops the current job immediately. Drain refuses new work while the
current job finishes. Resume ends drain on an idle node.

## 5. Dashboard

1. Build the UI: `cd web && npm ci && npm run build`.
2. **Live agent (preferred for demos):** start `node-agent` with
   `-dashboard 127.0.0.1:8080 -web web/dist` as above, then open
   `http://127.0.0.1:8080`. Status, probes, drain, and reclaim hit the real
   lease manager.
3. **Mock only (UI iteration):**  
   `go run ./cmd/dashboard-mock -addr 127.0.0.1:8080 -web web/dist`

Verification steps: [Live dashboard testing](dashboard-live-testing.md).  
API contract: [Dashboard API](dashboard-api.md).

## 6. Security review and failure drills

Before lending a real GPU:

1. Walk [Security checklist](security-checklist.md).
2. Practice the drills in [Failure cases](failure-cases.md) (crash exit,
   reclaim, drain).
3. On Linux, run `./scripts/smoke-podman.sh`.

## 7. Federation (registration and heartbeat)

Copy [examples/node-config.json](../examples/node-config.json) and pass it to
serve mode:

```sh
export NODE_API_KEY=...          # optional; overrides api_key in the file
/tmp/node-agent -serve -config /path/to/node-config.json
```

`-allowlist` on the command line overrides `allowlist` in the file. Unknown
fields are rejected, so a typo fails loudly instead of falling back to a default.

| Field | Default | Notes |
| --- | --- | --- |
| `coordinator_url` | none (local-only) | Setting it turns on registration and heartbeats |
| `provider_id` | lowercase hostname | Must stay the same across restarts (R2) |
| `accepted_tiers` | none, required | Job tiers you will run (R5) |
| `runtimes` | `[]` | Advertised alongside the detected `podman`, e.g. `"ollama"` (R8) |
| `cpu_only` | `false` | Let a machine with no GPU register (see below) |
| `heartbeat_interval` | `5s` | H2 |
| `lost_after` | `15s` | Must be longer than `heartbeat_interval` (H10) |
| `api_key` | none | Sent as `X-API-Key`; prefer `NODE_API_KEY` |

On startup the agent runs capability discovery (`nvidia-smi`, Podman) and
registers `gpu_model`, `vram_mb`, `driver_version`, and `runtimes` (R4). It
refuses leases until registration succeeds, then heartbeats on its own
goroutine. Heartbeat loss reclaims the current job and blocks new leases
until recovery. See [Failure cases](failure-cases.md#lost-heartbeat--coordinator-unreachable).

The agent refuses to start when it cannot register honestly:

- **No GPU** and `cpu_only` unset. The coordinator rejects `vram_mb = 0` (R3).
  With `cpu_only: true` the node registers `gpu_model: "cpu"`,
  `vram_mb: <system RAM>` and `driver_version: "none"`. Use this for a CPU
  demo, and only for jobs that fit in RAM.
- **No rootless Podman.** Every job runs in it.
- **A `4xx` registration reply** from the coordinator, e.g. a tier or runtime
  it does not accept.

For explicit capability overrides, start the agent with coordinator flags:

```sh
node-agent -serve -allowlist examples/allowlist.json \
  -coordinator http://127.0.0.1:8000 -provider-id "$(hostname)" \
  -gpu-model "NVIDIA GeForce RTX 4080" -vram-mb 16376 -driver 580.178.04 \
  -runtimes ollama -tiers standard
```

This mode uses `-api-key` or `NODE_API_KEY`. Use `-heartbeat` and
`-lost-after` to set the interval and timeout. If startup registration is
rejected, the socket stays open for local controls and the agent refuses
leases. Use one startup mode per invocation.

Start jobs through `gpu-ctl` or local offers. The
[coordinator requirements](coordinator-requirements.md) describe the lease
offer protocol for future polling.

## Related docs

| Topic | Document |
| --- | --- |
| Isolation flags and smoke test | [podman-execution.md](podman-execution.md) |
| Drain / reclaim / socket | [provider-controls.md](provider-controls.md) |
| Digest policy | [allowlist.md](allowlist.md) |
| GPU detection | [gpu-smi.md](gpu-smi.md) |
| Next engineering tasks | [way-forward.md](way-forward.md) |
