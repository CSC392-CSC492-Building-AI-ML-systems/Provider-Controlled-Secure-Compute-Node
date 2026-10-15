# Use of AI tools

Per the course policy, this file records where and how generative AI was used in
this repository. The group is accountable for all of it and should be able to
explain and modify any of the code listed here.

Add a row whenever AI produces or substantially shapes code or written content.
Say what the tool did and what the member did with the output.

| Date | Tool | Used for | Files / sections | Member | Where drafts are kept |
| --- | --- | --- | --- | --- | --- |
| 2026-10-14 | Cursor AI assistant | Generated the dashboard foundation from a plan the member reviewed and approved: the API contract, the Go dashboard server (types, probe registry, event stream, HTTP handlers, mock data, tests), the mock server command, and the React API client and panels. The member reviewed the design, chose the stack and branch, and ran the tests and the end-to-end check. | `docs/dashboard-api.md`, `internal/dashboard/`, `cmd/dashboard-mock/`, `web/src/`, `web/vite.config.ts`, `web/README.md`, `.gitignore` | Bhavya | Chat history and saved plan, kept by Bhavya |
| 2026-10-07 to 2026-10-14 | Cursor AI assistant | Brainstorming and explanations: capability discovery task breakdown, capability report fields, `nvidia-smi`/NVML/CDI concepts, interpretation of the mentor's dashboard mockups, work split between groups. Planning only; no submitted text. | None | Bhavya | Chat history, kept by Bhavya |

## Not AI-generated

- `web/` project scaffolding (`tsconfig*.json`, `.oxlintrc.json`, `web/.gitignore`,
  `public/favicon.svg`, `package-lock.json`) comes from the official Vite template
  (`npm create vite`, React + TypeScript).
- `go.mod` is copied unchanged from the `feat/heartbeat-lease` branch.
