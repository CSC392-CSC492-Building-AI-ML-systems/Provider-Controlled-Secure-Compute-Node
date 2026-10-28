# Use of AI tools

Per the course policy, this file records where and how generative AI was used in
this repository. The group is accountable for all of it and should be able to
explain and modify any of the code listed here.

Add a row whenever AI produces or substantially shapes code or written content.
Say what the tool did and what the member did with the output.

## Going forward

For every future change where AI produces or substantially shapes code or written
content, append an entry to this table before considering the change complete.
Include the date, tool, purpose, affected files or sections, responsible member,
and where the prompts or drafts can be reviewed. Keep the entry specific enough
to distinguish generated output from the member's decisions, edits, testing,
and verification. Changes made without AI do not need an entry.

| Date | Tool | Used for | Files / sections | Member | Where drafts are kept |
| --- | --- | --- | --- | --- | --- |
| 2026-10-27 | Codex (GPT-5) | Implemented Task 3 dashboard capability panels: polished the VRAM, CDI, rootless-container, and model-mount states; added the Host & GPU details panel; expanded the validation matrix with messages and timestamps; and added JSON report export. Codex also added responsive/error/no-GPU presentation styles. The member supplied the task requirements and reviewed the implementation direction. Codex verified the production frontend build and lint, focused Go tests, the mock dashboard visually, full-audit rendering, and the export interaction. | `web/src/panels/StatusCards.tsx`, `web/src/panels/HostDetails.tsx`, `web/src/panels/ValidationMatrix.tsx`, `web/src/App.tsx`, `web/src/api.ts`, `web/src/index.css` | Jayant Saini | Chat history, kept by Jayant Saini |
| 2026-10-27 | Codex (GPT-5) | Proposed and implemented the dashboard contract and mock support for model metadata (`name`, SHA-256, and size), normalized empty model lists to `[]`, and added representative mock model and model-mount probe values for frontend development. The model values are explicitly documented as illustrative rather than machine-detected. | `docs/dashboard-api.md` (`GET /api/capabilities`), `internal/dashboard/types.go`, `internal/dashboard/server.go`, `internal/dashboard/server_test.go`, `internal/dashboard/mock.go` | Jayant Saini | Chat history, kept by Jayant Saini |
| 2026-10-27 | Codex (GPT-5) | Documented Task 3 assumptions, mock-versus-real data boundaries, report-export behavior, and remaining backend model-discovery/hash work after the member requested that placeholder values and assumptions be made explicit. | `notes.txt` | Jayant Saini | Chat history, kept by Jayant Saini |
| 2026-10-27 | Codex (GPT-5) | Updated GPU metric parsing so NVIDIA `[N/A]` and `[Not Supported]` values map to zero in the existing numeric API fields, added focused parser tests, and documented the design decision and its tradeoff. | `internal/capability/gpu_smi.go`, `internal/capability/gpu_smi_test.go`, `docs/design-choices.md` | Jayant Saini | Chat history, kept by Jayant Saini |
| 2026-10-27 | Codex (GPT-5) | Integrated runtime and host detection into `GPUSMICollector.Capabilities`, preserving partial GPU results by recording runtime/host failures in `probe_errors`. The member requested this integration after reviewing the separation between collectors and probes. | `internal/capability/gpu_smi.go` (`Capabilities`) | Jayant Saini | Chat history, kept by Jayant Saini |
| 2026-10-15 | Codex (GPT-5) | Implemented NVIDIA GPU capability discovery: run plain `nvidia-smi` and the CSV GPU query, parse driver/CUDA versions and GPU metadata, and return the dashboard capability report. The member reviewed the implementation and verified the focused Go package test; the broader dashboard test was blocked by the sandbox preventing localhost listeners. | `internal/capability/gpu_smi.go` (`Capabilities` and parsing helpers) | Jayant Saini | Chat history, kept by Jayant Saini |
| 2026-10-15 | Codex (GPT-5) | Implemented live NVIDIA GPU metrics collection: query and parse UUID, used/free VRAM, utilization, and temperature, then return timestamped dashboard metrics. The member reviewed the implementation and verified the focused Go package test. | `internal/capability/gpu_smi.go` (`Metrics` and `parseMetricsQuery`) | Jayant Saini | Chat history, kept by Jayant Saini |
| 2026-10-15 | Codex (GPT-5) | Updated GPU metrics parsing and dashboard types to accept integer and decimal numeric values while preserving exact decimal precision. | `internal/capability/gpu_smi.go`, `internal/dashboard/types.go`, `internal/dashboard/mock.go`, `docs/gpu-smi.md` | Jayant Saini | Chat history, kept by Jayant Saini |
| 2026-10-15 | Codex (GPT-5) | Created high-level and function-by-function documentation for the NVIDIA GPU SMI capability collector. The member reviewed the generated documentation. | `docs/gpu-smi.md` | Jayant Saini | Chat history, kept by Jayant Saini |
| 2026-10-14 | Cursor AI assistant | Generated the dashboard foundation from a plan the member reviewed and approved: the API contract, the Go dashboard server (types, probe registry, event stream, HTTP handlers, mock data, tests), the mock server command, and the React API client and panels. The member reviewed the design, chose the stack and branch, and ran the tests and the end-to-end check. | `docs/dashboard-api.md`, `internal/dashboard/`, `cmd/dashboard-mock/`, `web/src/`, `web/vite.config.ts`, `web/README.md`, `.gitignore` | Bhavya | Chat history and saved plan, kept by Bhavya |
| 2026-10-07 to 2026-10-14 | Cursor AI assistant | Brainstorming and explanations: capability discovery task breakdown, capability report fields, `nvidia-smi`/NVML/CDI concepts, interpretation of the mentor's dashboard mockups, work split between groups. Planning only; no submitted text. | None | Bhavya | Chat history, kept by Bhavya |

## Not AI-generated

- `web/` project scaffolding (`tsconfig*.json`, `.oxlintrc.json`, `web/.gitignore`,
  `public/favicon.svg`, `package-lock.json`) comes from the official Vite template
  (`npm create vite`, React + TypeScript).
- `go.mod` is copied unchanged from the `feat/heartbeat-lease` branch.
