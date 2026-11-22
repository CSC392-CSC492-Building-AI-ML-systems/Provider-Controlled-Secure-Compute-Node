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

Do not combine `-serve` with `-interactive`. A loopback `-dashboard` flag is
planned when the live dashboard is wired into the agent; until then use
`dashboard-mock` for the UI (see below).

Details: [Podman execution](podman-execution.md),
[Provider controls](provider-controls.md).

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
2. Run `go run ./cmd/dashboard-mock -addr 127.0.0.1:8080 -web web/dist` for
   UI development against mock data (loopback only).
3. Open `http://127.0.0.1:8080`. Live agent wiring (`node-agent -dashboard`)
   is the next integration step after this guide.

API contract: [Dashboard API](dashboard-api.md).

## 6. Security review and failure drills

Before lending a real GPU:

1. Walk [Security checklist](security-checklist.md).
2. Practice the drills in [Failure cases](failure-cases.md) (crash exit,
   reclaim, drain).
3. On Linux, run `./scripts/smoke-podman.sh`.

## 7. Federation (when coordinator is available)

Connect the long-running agent to a coordinator with flags:

```bash
node-agent -serve -allowlist examples/allowlist.json \
  -coordinator http://127.0.0.1:8000 -provider-id "$(hostname)" \
  -gpu-model "NVIDIA GeForce RTX 4080" -vram-mb 16376 -driver 580.178.04 \
  -runtimes ollama -tiers standard
```

The API key comes from `-api-key` or `NODE_API_KEY`. `-heartbeat` (default
5s) and `-lost-after` (default 15s) tune the heartbeat. While the coordinator
is unreachable the agent refuses new work; see
[failure cases](failure-cases.md#lost-heartbeat--coordinator-unreachable).
Offer polling is not wired yet, so jobs still start through `gpu-ctl` or local
offers. Protocol details are in
[coordinator requirements](coordinator-requirements.md).

## Related docs

| Topic | Document |
| --- | --- |
| Isolation flags and smoke test | [podman-execution.md](podman-execution.md) |
| Drain / reclaim / socket | [provider-controls.md](provider-controls.md) |
| Digest policy | [allowlist.md](allowlist.md) |
| GPU detection | [gpu-smi.md](gpu-smi.md) |
| Next engineering tasks | [way-forward.md](way-forward.md) |
