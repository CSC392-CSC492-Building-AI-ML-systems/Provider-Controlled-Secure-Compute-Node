#!/bin/sh

# Starts the curated Ollama image in one of two modes:
#
#   job    Run one inference job from /job/input and exit (default). This is
#          what the node agent uses; see JOB_CONTRACT.md.
#   serve  Run the Ollama HTTP API until stopped, for local development.

set -eu

# The approved model comes from the image, never from the runtime
# environment, and models are only read from the image's model directory.
# shellcheck disable=SC1091
. /usr/local/share/provider-node/approved-model.env
export APPROVED_MODEL
export OLLAMA_MODELS=/models
export OLLAMA_NOPRUNE=1
# Debug logging would record prompts in the container log.
unset OLLAMA_DEBUG

usage() {
    echo "usage: start-ollama [job|serve]" >&2
    exit 2
}

if [ "$#" -gt 1 ]; then
    usage
fi

case "${1:-job}" in
    job)
        # The job runner verifies the model itself so that a failed check is
        # also reported in response.json.
        exec /usr/local/bin/ollama-job
        ;;
    serve)
        export OLLAMA_HOST="${OLLAMA_HOST:-0.0.0.0:11434}"
        /usr/local/bin/verify-approved-model
        exec /bin/ollama serve
        ;;
    *)
        usage
        ;;
esac
