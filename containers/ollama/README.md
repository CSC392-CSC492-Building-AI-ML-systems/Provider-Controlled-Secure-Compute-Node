# Curated Ollama Inference Container

This directory contains the baseline Ollama service image for the provider
node. It starts the Ollama HTTP API in a rootless Podman container and is
intended to be the foundation for the approved model and GPU configuration.

The base image is Ollama `0.40.6`, pinned to its immutable multi-architecture
OCI digest. This prevents a later tag update from silently changing the
software that runs on a provider node.

## Current scope

This initial image:

- starts `ollama serve`;
- listens on the container's port `11434`;
- includes a container health check; and
- persists Ollama data in a named Podman volume.

It does not bundle an inference model or configure GPU access yet. Those
features require separate approval and testing.

## Requirements

- Podman
- `curl`, for the host-side readiness check

Podman on macOS runs containers inside a Linux virtual machine. This image can
be built and CPU-tested there, but GPU passthrough must be tested on the target
Linux provider node.

## Build

Run from the repository root:

```sh
podman build \
  --tag localhost/provider-node-ollama:0.1.0 \
  --file containers/ollama/Containerfile \
  containers/ollama
```

## Run

```sh
podman run --rm \
  --name provider-node-ollama \
  --publish 127.0.0.1:11434:11434 \
  --volume ollama-models:/root/.ollama:Z \
  localhost/provider-node-ollama:0.1.0
```

Binding to `127.0.0.1` prevents the API from being exposed on every host
network interface.

## Verify readiness

In another terminal:

```sh
curl --fail --silent --show-error \
  http://127.0.0.1:11434/api/tags
```

A successful request returns JSON containing the models currently available
in the `ollama-models` volume. An empty model list is expected until an
approved model-provisioning workflow is added.

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
can shut down cleanly.
