# Curated Ollama Inference Container

This directory contains the Ollama service image for the provider node. The
image starts the Ollama HTTP API in a rootless Podman container and serves one
approved model from a Podman volume.

The base image is Ollama `0.40.6`, pinned to its immutable multi-architecture
OCI digest. This prevents a later tag update from silently changing the
software that runs on a provider node.

## Approved model

The approved development model is **TinyLlama 1.1B**:

| Property | Value |
| --- | --- |
| Ollama identifier | `tinyllama:1.1b` |
| Format | GGUF, `Q4_0` quantization |
| Parameters | 1.1 billion |
| Context length | 2,048 tokens |
| Weight digest | `sha256:2af3b81862c6be03c769683af18efdadb2c33f60ff32ab6f83e42c043d6c7816` |
| Download size | 638 MB (637,699,456 bytes) |
| Disk space to reserve | 1 GB |
| Memory to reserve | 2 GB minimum, 4 GB recommended for CPU inference |
| License | Apache License 2.0 |

TinyLlama is a compact Llama-architecture model published by the TinyLlama
project. Its project license is Apache License 2.0:

- Project: <https://github.com/jzhang38/TinyLlama>
- License: <https://github.com/jzhang38/TinyLlama/blob/main/LICENSE>
- Ollama library entry: <https://ollama.com/library/tinyllama:1.1b>

The model is small enough for a CPU smoke test. It is a development model, not
the final production model. Replacing it requires editing
`approved-model.env`, rebuilding the image, and provisioning the new model.
That rebuild changes the image digest.

The model is stored in the named Podman volume `ollama-models`, not copied
into the image. The volume is mounted read-only while the service runs, so a
request cannot save a different model into it. Startup hashes every file the
model's manifest refers to (weights, prompt template, system prompt,
parameters and config) with SHA-256, and rejects a missing model, a digest
mismatch, a manifest that refers to any other file, or any additional model
manifest, including a symlinked one. All five digests are pinned in
`approved-model.env`.

## Current scope

The image has two modes, selected by the container command:

- **`job` (default)** runs one inference job and exits. The request is read
  from `/job/input/request.json` and the result is written to
  `/job/output/response.json`. Ollama listens only on the container's
  loopback interface, so the container needs no network. This is the mode the
  node agent uses. The request and response formats, limits and exit codes
  are defined in [JOB_CONTRACT.md](JOB_CONTRACT.md).
- **`serve`** runs the Ollama HTTP API on port `11434` until it is stopped.
  It is meant for local development and manual testing.

Both modes refuse to start Ollama unless the model volume holds exactly the
pinned TinyLlama model. The image includes a health check that confirms Ollama
is responding; model integrity is checked once at startup, not on every
health check.

The image is designed to run the way the node agent runs workloads: as
UID/GID `65534:65534`, with a read-only root filesystem, all capabilities
dropped and no network. `HOME` points at `/tmp`, Ollama does not prune the
read-only model volume, and provisioning makes the model files readable by
that user.

GPU passthrough is not configured yet. The model is not downloaded during
`podman build`.

## Requirements

- Podman
- `curl`, for the host-side readiness check in `serve` mode
- Internet access while building the image and provisioning the model
- About 1 GB of free disk space and 2 GB of available memory

Podman on macOS runs containers inside a Linux virtual machine. This image can
be built and CPU-tested there, but GPU passthrough must be tested on the target
Linux provider node.

## Build

Run from the repository root:

```sh
podman build \
  --tag localhost/provider-node-ollama:0.3.0 \
  --file containers/ollama/Containerfile \
  containers/ollama
```

The build compiles the job runner in `jobrunner/` with a digest-pinned Go
image, then copies the static binary into the Ollama image.

## Provision the approved model

The provisioning command accepts no model name. It always installs the model
pinned in the image and checks every pinned digest afterward. The
provisioning container runs with all Linux capabilities dropped and
`no-new-privileges`.

```sh
containers/ollama/provision-model.sh
```

This creates the `ollama-models` volume if needed and downloads about 638 MB.
If the pinned model is already present, the command leaves it unchanged
except for making it readable by the inference user. If a different or
modified model is already present, it stops without downloading anything. If
a new download fails partway or fails verification, its files are removed so
the next provisioning run can try again.

## Run one inference job

Create a job directory. The container user must be able to read the request
and write to the output directory:

```sh
job=$(mktemp -d)
mkdir "$job/input" "$job/output"
printf '%s\n' '{"prompt": "Explain what a GPU is in one sentence."}' \
  > "$job/input/request.json"
chmod 0755 "$job" "$job/input"
chmod 0644 "$job/input/request.json"
chmod 0777 "$job/output"
```

Run the job with the same restrictions the node agent uses:

```sh
podman run --rm \
  --user 65534:65534 \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --network none \
  --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,nodev,size=64m,mode=1777 \
  --memory 2g \
  --pids-limit 128 \
  --volume ollama-models:/models:ro,Z \
  --volume "$job/input:/job/input:ro,Z" \
  --volume "$job/output:/job/output:rw,Z" \
  localhost/provider-node-ollama:0.3.0
echo "exit code: $?"
cat "$job/output/response.json"
```

The exit code and `response.json` report the outcome; see
[JOB_CONTRACT.md](JOB_CONTRACT.md). Delete the job directory afterward, since
it contains the prompt and the response:

```sh
rm -rf "$job"
```

## Run the API server (development)

```sh
podman run --rm \
  --name provider-node-ollama \
  --user 65534:65534 \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,nodev,size=64m,mode=1777 \
  --publish 127.0.0.1:11434:11434 \
  --volume ollama-models:/models:ro,Z \
  localhost/provider-node-ollama:0.3.0 serve
```

Binding to `127.0.0.1` prevents the API from being exposed on every host
network interface. The read-only model mount prevents the running container
from installing another model.

The container exits before serving requests when the approved model is missing
or does not match the pinned digest.

### Verify readiness

In another terminal:

```sh
curl --fail --silent --show-error \
  http://127.0.0.1:11434/api/tags
```

The response should contain `tinyllama:1.1b` and no other model.

You can also inspect the container health status:

```sh
podman inspect \
  --format '{{.State.Health.Status}}' \
  provider-node-ollama
```

### Stop

Press `Ctrl+C` in the terminal running the container, or run:

```sh
podman stop provider-node-ollama
```

The entrypoint uses `exec`, so Ollama receives the stop signal directly and
can shut down cleanly. The model remains in the `ollama-models` volume for the
next run.

## Develop the job runner

The job runner is a small Go program with no dependencies outside the
standard library. Its tests use a fake Ollama server, so they run without
Podman or a model:

```sh
cd containers/ollama/jobrunner
go test ./...
```
