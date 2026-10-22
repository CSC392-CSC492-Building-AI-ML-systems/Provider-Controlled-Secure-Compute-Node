#!/bin/sh

# Confirms that the model directory contains only the pinned TinyLlama build.
# Exit status 10 means the approved model has not been provisioned yet.

set -eu

MODEL_ROOT=/models
CONFIG=/usr/local/share/provider-node/approved-model.env

if [ ! -f "$CONFIG" ]; then
    echo "Approved model configuration is missing: $CONFIG" >&2
    exit 20
fi

# shellcheck disable=SC1090
. "$CONFIG"

if [ -z "${APPROVED_MODEL:-}" ] ||
    [ -z "${APPROVED_MODEL_MANIFEST:-}" ] ||
    [ -z "${APPROVED_WEIGHT_DIGEST:-}" ]; then
    echo "Approved model configuration is incomplete." >&2
    exit 20
fi

manifest="$MODEL_ROOT/$APPROVED_MODEL_MANIFEST"
weight_blob="$MODEL_ROOT/blobs/sha256-${APPROVED_WEIGHT_DIGEST#sha256:}"

list_manifests() {
    root="$1"

    if [ ! -d "$root" ]; then
        return 0
    fi

    find "$root" -type f
}

unexpected_manifests() {
    list_manifests "$MODEL_ROOT/manifests" | while IFS= read -r path; do
        if [ "$path" != "$manifest" ]; then
            printf '%s\n' "$path"
        fi
    done
}

if [ ! -d "$MODEL_ROOT" ]; then
    echo "Approved model ${APPROVED_MODEL} is not provisioned." >&2
    echo "Run containers/ollama/provision-model.sh before starting the container." >&2
    exit 10
fi

unexpected="$(unexpected_manifests || true)"
if [ -n "$unexpected" ]; then
    echo "Only ${APPROVED_MODEL} may be installed. Unapproved manifests found:" >&2
    printf '%s\n' "$unexpected" >&2
    exit 20
fi

if [ -L "$manifest" ]; then
    echo "Approved model manifest must be a regular file: $manifest" >&2
    exit 20
fi

if [ ! -f "$manifest" ]; then
    echo "Approved model ${APPROVED_MODEL} is not provisioned." >&2
    echo "Run containers/ollama/provision-model.sh before starting the container." >&2
    exit 10
fi

if [ -L "$weight_blob" ] || [ ! -f "$weight_blob" ]; then
    echo "Pinned weights for ${APPROVED_MODEL} are missing: $weight_blob" >&2
    exit 20
fi

if ! grep -F -q "$APPROVED_WEIGHT_DIGEST" "$manifest"; then
    echo "Installed ${APPROVED_MODEL} does not match pinned digest ${APPROVED_WEIGHT_DIGEST}." >&2
    exit 20
fi
