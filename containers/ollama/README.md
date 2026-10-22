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
request cannot save a different model into it. Startup hashes the weight file
with SHA-256 and rejects a missing model, a digest mismatch, or any additional
model manifest.

## Current scope

This image:

- starts `ollama serve`;
- listens on the container's port `11434`;
- requires the pinned TinyLlama model before startup;
- checks model integrity from the model volume; and
- includes a container health check.

GPU passthrough is not configured yet. The model is not downloaded during
`podman build`.

## Requirements

- Podman
- `curl`, for the host-side readiness check
- Internet access while provisioning the model
- About 1 GB of free disk space and 2 GB of available memory

Podman on macOS runs containers inside a Linux virtual machine. This image can
be built and CPU-tested there, but GPU passthrough must be tested on the target
Linux provider node.

## Build

Run from the repository root:

```sh
podman build \
  --tag localhost/provider-node-ollama:0.2.0 \
  --file containers/ollama/Containerfile \
  containers/ollama
```

## Provision the approved model

The provisioning command accepts no model name. It always installs the model
pinned in the image and checks its weight digest afterward.

```sh
containers/ollama/provision-model.sh
```

This creates the `ollama-models` volume if needed and downloads about 638 MB.
If the pinned model is already present, the command leaves it unchanged. If a
different or modified model is present, it stops without downloading anything.

## Run

```sh
podman run --rm \
  --name provider-node-ollama \
  --publish 127.0.0.1:11434:11434 \
  --volume ollama-models:/models:ro,Z \
  localhost/provider-node-ollama:0.2.0
```

Binding to `127.0.0.1` prevents the API from being exposed on every host
network interface. The read-only model mount prevents the running container
from installing another model.

The container exits before serving requests when the approved model is missing
or does not match the pinned digest.

## Verify readiness

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

## Stop

Press `Ctrl+C` in the terminal running the container, or run:

```sh
podman stop provider-node-ollama
```

The entrypoint uses `exec`, so Ollama receives the stop signal directly and
can shut down cleanly. The model remains in the `ollama-models` volume for the
next run.
