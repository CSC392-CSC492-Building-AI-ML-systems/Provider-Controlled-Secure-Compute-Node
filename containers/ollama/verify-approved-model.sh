#!/bin/sh

# Confirms that the model directory contains only the pinned TinyLlama build.
# Every file the manifest refers to (config, weights, template, system prompt
# and parameters) must be present and match its pinned SHA-256 digest.
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
    [ -z "${APPROVED_WEIGHT_DIGEST:-}" ] ||
    [ -z "${APPROVED_BLOB_DIGESTS:-}" ]; then
    echo "Approved model configuration is incomplete." >&2
    exit 20
fi

# shellcheck disable=SC2086 # split the digest list into one digest per line
expected_digests="$(printf '%s\n' $APPROVED_BLOB_DIGESTS | sort -u)"
case "$expected_digests" in
    *"$APPROVED_WEIGHT_DIGEST"*) ;;
    *)
        echo "Approved model configuration does not pin the weight file." >&2
        exit 20
        ;;
esac

manifest="$MODEL_ROOT/$APPROVED_MODEL_MANIFEST"

blob_path() {
    printf '%s/blobs/sha256-%s\n' "$MODEL_ROOT" "${1#sha256:}"
}

# Anything that is not a directory counts, so a symlinked manifest cannot
# hide another model from this check.
list_manifests() {
    root="$1"

    if [ ! -d "$root" ]; then
        return 0
    fi

    find "$root" ! -type d
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

referenced_digests="$(grep -o 'sha256:[a-f0-9]\{64\}' "$manifest" | sort -u)"
if [ "$referenced_digests" != "$expected_digests" ]; then
    echo "Installed ${APPROVED_MODEL} manifest does not reference exactly the pinned files." >&2
    exit 20
fi

for digest in $expected_digests; do
    blob="$(blob_path "$digest")"

    if [ -L "$blob" ] || [ ! -f "$blob" ]; then
        echo "Pinned file for ${APPROVED_MODEL} is missing: $blob" >&2
        exit 20
    fi

    actual_digest="$(sha256sum "$blob" | awk '{print $1}')"
    if [ "$actual_digest" != "${digest#sha256:}" ]; then
        echo "File for ${APPROVED_MODEL} does not match its pinned digest: $blob" >&2
        echo "Expected ${digest}, got sha256:${actual_digest}." >&2
        exit 20
    fi
done
