#!/bin/sh

set -eu

export OLLAMA_HOST="${OLLAMA_HOST:-0.0.0.0:11434}"

exec /bin/ollama serve
