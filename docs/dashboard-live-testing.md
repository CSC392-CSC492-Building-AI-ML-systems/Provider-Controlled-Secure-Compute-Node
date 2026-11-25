# Live dashboard verification

How to confirm that `node-agent -serve -dashboard` talks to the real lease
manager (not `dashboard-mock`).

## Automated

From the repository root:

```sh
go test ./internal/dashboard/ ./internal/allowlist/ ./internal/control/ ./internal/lease/ ./cmd/node-agent/
go test -race ./internal/dashboard/ ./cmd/node-agent/
go vet ./cmd/node-agent/ ./internal/dashboard/
```

Expect green tests, including `TestServeDashboardStatusOnLoopback` and
`TestRefuseNonLoopbackDashboard`.

## Manual smoke (no GPU required for API checks)

1. Build the agent, CLI, and UI:

   ```sh
   go build -o /tmp/node-agent ./cmd/node-agent
   go build -o /tmp/gpu-ctl ./cmd/gpu-ctl
   cd web && npm ci && npm run build && cd ..
   ```

2. Start a local-only agent with the live dashboard (loopback only):

   ```sh
   /tmp/node-agent -serve -allowlist examples/allowlist.json \
     -dashboard 127.0.0.1:8080 -web web/dist
   ```

3. In a second terminal:

   ```sh
   curl -s http://127.0.0.1:8080/api/status
   curl -s http://127.0.0.1:8080/api/capabilities
   curl -s http://127.0.0.1:8080/api/probes
   curl -s -X POST http://127.0.0.1:8080/api/probes/run
   curl -s -X POST http://127.0.0.1:8080/api/control/drain \
     -H 'content-type: application/json' -d '{}'
   /tmp/gpu-ctl status
   curl -s -X POST http://127.0.0.1:8080/api/control/reclaim \
     -H 'content-type: application/json' -d '{}'
   ```

4. Open `http://127.0.0.1:8080` — status cards should match `gpu-ctl status`;
   Drain / Reclaim should change node state; the probe matrix should run.

5. Safety check — non-loopback must be refused:

   ```sh
   /tmp/node-agent -serve -allowlist examples/allowlist.json \
     -dashboard 0.0.0.0:8080
   ```

   Expect a startup error mentioning a non-loopback address.

6. Optional (Linux + allowlisted image): `gpu-ctl run`, watch `/api/status`
   move to `BUSY`, then reclaim from the UI.

## Pass criteria

| Check | Pass |
| --- | --- |
| `-serve` without `-dashboard` | `gpu-ctl status` still works |
| `-dashboard` on loopback | `GET /api/status` returns JSON with `provider_id` and `state` |
| Drain via API or UI | state becomes `DRAIN`; new local offers are rejected |
| Events | diagnostic stream shows lease/control messages when work runs |
| Non-loopback `-dashboard` | agent refuses to start |
| Automated tests above | green |

## Mock vs live

| Mode | Command |
| --- | --- |
| Mock UI (fake node) | `go run ./cmd/dashboard-mock -addr 127.0.0.1:8080 -web web/dist` |
| Live agent | `node-agent -serve … -dashboard 127.0.0.1:8080 -web web/dist` |

Use the mock for front-end iteration; use `-dashboard` on the agent for demos
that must drain/reclaim real leases.
