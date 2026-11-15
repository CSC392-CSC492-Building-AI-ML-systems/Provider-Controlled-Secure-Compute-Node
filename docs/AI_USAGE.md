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
| 2026-11-14 | Claude Code (Claude Opus 5.5) | Implemented the local provider CLI: fixed reclaim during a running job, added `Resume`, the Unix-socket control server, `node-agent -serve`, and the `gpu-ctl` commands, with tests and docs. The member reviewed the changes and ran the tests. | `internal/lease/manager.go`, `internal/allowlist/checker.go`, `internal/control/`, `cmd/node-agent/`, `cmd/gpu-ctl/`, `docs/provider-controls.md` | Sanjay Ram | Chat history, kept by Sanjay Ram |
| 2026-11-14 | Claude Code (Claude Opus 5.5) | Reviewed PR 9 and fixed security and reliability issues in the Ollama container scripts. The member reviewed the diff. | `containers/ollama/` | Sanjay Ram | Chat history, kept by Sanjay Ram |
| 2026-10-22 | Claude Code (Claude Opus 5.5) | Implemented the image digest allowlist and its tests. The member reviewed the implementation. | `internal/allowlist/`, `docs/allowlist.md` | Sanjay Ram | Chat history, kept by Sanjay Ram |
| 2026-10-15 to 2026-11-14 | Claude Code (Claude Opus 5.5) | Explanations of the repo, lease manager, Go, image digests, and rootless Podman. Planning only; no submitted text. | None | Sanjay Ram | Chat history, kept by Sanjay Ram |
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
