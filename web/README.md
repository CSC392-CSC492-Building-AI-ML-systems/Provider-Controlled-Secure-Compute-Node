# Dashboard

React + Vite UI for the compute node. It reads the API described in
[`docs/dashboard-api.md`](../docs/dashboard-api.md).

## Run in development

Start the mock API from the repo root:

```bash
go run ./cmd/dashboard-mock
```

Then, in `web/`:

```bash
npm install
npm run dev
```

Open the URL Vite prints. Requests to `/api` are proxied to `127.0.0.1:8080`.

## Build

```bash
npm run build
```

`dashboard-mock` serves `web/dist` at `/` when it exists, so after a build the
whole dashboard is available at `http://127.0.0.1:8080`.

## Layout

| File | Owner |
| --- | --- |
| `src/api.ts` | shared (mirrors the contract) |
| `src/panels/StatusCards.tsx` | capability (VRAM, CDI, sandbox cards), heartbeat-lease (agent state, mesh IP) |
| `src/panels/ValidationMatrix.tsx` | capability |
| `src/panels/ProviderControls.tsx` | heartbeat-lease |
| `src/panels/DiagnosticStream.tsx` | heartbeat-lease |
