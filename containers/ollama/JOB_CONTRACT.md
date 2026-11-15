# Inference Job Contract

This document defines how one inference job enters the curated Ollama
container and how its result leaves it. It is the interface between this image
and the node agent's container engine.

## Model

One container runs **one job** and then exits:

1. The container starts with the job's request mounted read-only.
2. It verifies the approved model, starts Ollama on the container's loopback
   interface, and generates one response.
3. It writes the result to the output directory and exits.

This matches how the node agent supervises a lease: it waits for the container
to exit and reports the exit code. The container needs no network access,
because Ollama listens on `127.0.0.1` inside the container and only the job
runner talks to it.

The long-running HTTP server is still available as `serve` mode for local
development (see [README.md](README.md)). The node agent uses job mode, which
is the image's default command.

## Mounts

| Container path | Access | Contents |
| --- | --- | --- |
| `/models` | read-only | The provisioned model volume (`ollama-models`) |
| `/job/input/request.json` | read-only | The request, written by the node agent |
| `/job/output/` | read-write | Empty directory; receives `response.json` |

The container runs as UID/GID `65534:65534`. The node agent must make
`request.json` readable and `/job/output` writable by that user.

## Request

`/job/input/request.json`:

```json
{
  "prompt": "Explain what a GPU is in one sentence.",
  "options": {
    "num_predict": 128,
    "temperature": 0.2,
    "top_p": 0.9,
    "top_k": 40,
    "seed": 42
  }
}
```

| Field | Required | Rule |
| --- | --- | --- |
| `prompt` | yes | Non-blank UTF-8 text, at most 8,192 bytes, no NUL characters |
| `options.num_predict` | no | Maximum generated tokens, 1–1,024; default 256 |
| `options.temperature` | no | 0–2 |
| `options.top_p` | no | Greater than 0, at most 1 |
| `options.top_k` | no | 1–100 |
| `options.seed` | no | Any integer |

The whole file may be at most 16 KiB and must contain exactly one JSON object.
Unknown fields are rejected. In particular, a request cannot choose a model,
replace the system prompt or template, or pass other Ollama options. The model
is fixed by the image.

## Response

`/job/output/response.json` on success:

```json
{
  "status": "ok",
  "model": "tinyllama:1.1b",
  "response": "A GPU is a processor designed to ...",
  "done_reason": "stop",
  "truncated": false,
  "processor": "cpu",
  "metrics": {
    "prompt_tokens": 14,
    "completion_tokens": 38,
    "total_duration_ms": 2140
  }
}
```

- `response` is never empty. An empty generation is reported as an error.
- `response` is at most 64 KiB. Longer output is cut at a character boundary
  and `truncated` is `true`.
- `done_reason` is Ollama's reason for stopping, such as `stop` or `length`.
- `processor` is `gpu`, `cpu`, `partial` (split between both) or `unknown`.

On failure:

```json
{
  "status": "error",
  "model": "tinyllama:1.1b",
  "error": {
    "code": "invalid_request",
    "message": "prompt is required"
  }
}
```

Error messages never contain the prompt or generated text.

## Exit codes

The exit code and `error.code` always agree, so the node agent can rely on
the exit code alone if it does not read the output.

| Exit code | `error.code` | Meaning |
| --- | --- | --- |
| 0 | — | Success; `response.json` has `status: ok` |
| 2 | `invalid_request` | Missing, oversized or malformed request |
| 10 | `model_not_provisioned` | The model volume does not contain the approved model |
| 20 | `model_verification_failed` | The model volume contains a modified or unapproved model |
| 30 | `server_start_failed` | Ollama did not start or become ready |
| 31 | `inference_failed` | Generation failed, timed out or returned nothing |
| 32 | `gpu_unavailable` | A GPU was required but inference ran on the CPU |
| 40 | — | `response.json` could not be written; details on stderr |
| 143 | `cancelled` | The container was stopped with `SIGTERM` during the job |

Generation is limited to 10 minutes. The lease expiry enforced by the node
agent may end the job earlier; in that case the container is killed and no
response is written.

## Sensitive data

- The request is mounted read-only, and the job runner never logs the prompt
  or the generated text.
- Ollama runs with debug logging disabled, so its request logs contain no
  prompt contents.
- The container's root filesystem is read-only. Apart from `response.json`,
  the only writable location is the `/tmp` tmpfs, which is discarded with the
  container.
- The node agent must delete the job's input and output directories after it
  has collected the result.

## Requirements for the node agent

These are not implemented in the container engine yet. They are listed here
so the engine can be integrated with this image later:

- Mount the model volume read-only at `/models`.
- Create a per-job directory, write `request.json`, mount it as described
  above, and delete it after the job.
- Allow at least 2 GB of memory; TinyLlama does not fit in 512 MB.
- Pass the GPU device when GPU inference is wanted (see the README).
- Carry the prompt in the lease, or fetch it from the coordinator. The current
  lease offer only contains an ID, an image digest and an expiry time.
