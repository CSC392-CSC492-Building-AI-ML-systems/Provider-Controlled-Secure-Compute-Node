#!/bin/sh

set -eu

# The image decides where approved models are read from. A runtime override
# cannot redirect Ollama to a directory containing a different model.
export OLLAMA_HOST="${OLLAMA_HOST:-0.0.0.0:11434}"
export OLLAMA_MODELS=/models

/usr/local/bin/verify-approved-model

exec /bin/ollama serve
