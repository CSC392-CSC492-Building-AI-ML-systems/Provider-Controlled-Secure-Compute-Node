#!/bin/sh

# Prepares the Podman model volume used by the curated Ollama container.
# Run this from the repository root after building the image.

set -eu

if [ "$#" -ne 0 ]; then
    echo "provision-model.sh accepts no arguments." >&2
    echo "It installs only the model pinned in the container image." >&2
    exit 2
fi

image="${OLLAMA_IMAGE:-localhost/provider-node-ollama:0.2.0}"
volume="${OLLAMA_VOLUME:-ollama-models}"

if ! command -v podman >/dev/null 2>&1; then
    echo "Podman is required to provision the approved model." >&2
    exit 1
fi

podman volume create "$volume" >/dev/null

exec podman run --rm \
    --volume "${volume}:/models:Z" \
    --entrypoint /usr/local/bin/provision-approved-model \
    "$image"
