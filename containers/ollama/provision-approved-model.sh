#!/bin/sh

# Installs the model selected in approved-model.env. This accepts no model
# argument, so it cannot be used to provision an arbitrary model.

set -eu

if [ "$#" -ne 0 ]; then
    echo "provision-approved-model accepts no arguments." >&2
    echo "The approved model is fixed by the container image." >&2
    exit 2
fi

export OLLAMA_HOST=127.0.0.1:11434
export OLLAMA_MODELS=/models

# shellcheck disable=SC1091
. /usr/local/share/provider-node/approved-model.env

set +e
/usr/local/bin/verify-approved-model
status=$?
set -e

case "$status" in
    0)
        echo "Approved model ${APPROVED_MODEL} is already provisioned."
        exit 0
        ;;
    10)
        ;;
    *)
        exit "$status"
        ;;
esac

mkdir -p "$OLLAMA_MODELS"
log=/tmp/ollama-provision.log
/bin/ollama serve >"$log" 2>&1 &
server_pid=$!

cleanup() {
    if kill -0 "$server_pid" 2>/dev/null; then
        kill -TERM "$server_pid" 2>/dev/null || true
        wait "$server_pid" || true
    fi
}

trap cleanup EXIT INT TERM

ready=0
attempt=0
while [ "$attempt" -lt 30 ]; do
    if /bin/ollama list >/dev/null 2>&1; then
        ready=1
        break
    fi

    if ! kill -0 "$server_pid" 2>/dev/null; then
        echo "Ollama stopped before it was ready to provision the model." >&2
        cat "$log" >&2
        exit 1
    fi

    attempt=$((attempt + 1))
    sleep 1
done

if [ "$ready" -ne 1 ]; then
    echo "Timed out waiting for Ollama to start." >&2
    cat "$log" >&2
    exit 1
fi

echo "Downloading approved model ${APPROVED_MODEL}."
/bin/ollama pull "$APPROVED_MODEL"
/usr/local/bin/verify-approved-model
echo "Verified approved model ${APPROVED_MODEL}."
