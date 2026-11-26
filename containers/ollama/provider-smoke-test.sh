#!/usr/bin/env bash

# Exercise the requester CLI, provider allowlist, and rootless Ollama runner.
set -euo pipefail

script_dir=$(cd "$(dirname "$0")" && pwd)
cd "$script_dir/../.."

reference=${1:-}
if [[ $# != 1 || ! $reference =~ ^(localhost|[a-z0-9]+([.-][a-z0-9]+)+)(:[0-9]+)?/[a-z0-9]+([._-][a-z0-9]+)*(/[a-z0-9]+([._-][a-z0-9]+)*)*@sha256:[a-f0-9]{64}$ ]]; then
    echo "Usage: $0 REGISTRY/REPOSITORY@sha256:DIGEST" >&2
    exit 2
fi

[[ $(podman --remote=false info --format '{{.Host.Security.Rootless}}') == true ]] || {
    echo "Run this test as a rootless Podman user." >&2
    exit 1
}

work=$(mktemp -d /tmp/ollama-provider.XXXXXX)
agent_pid=
cleanup() {
    status=$?
    if [[ -n $agent_pid ]]; then
        kill "$agent_pid" 2>/dev/null || true
        wait "$agent_pid" 2>/dev/null || true
    fi
    if [[ $status != 0 ]]; then
        for log in "$work/agent.log" "$work/result.ndjson"; do
            [[ ! -f $log ]] || cat "$log" >&2
        done
    fi
    rm -rf -- "$work"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

digest=${reference##*@}
jq -n --arg image "${reference%@*}" --arg digest "$digest" \
    '{images: [{image: $image, digest: $digest}]}' > "$work/allowlist.json"
export NODE_JOB_TOKEN
NODE_JOB_TOKEN=$(openssl rand -hex 32)

go build -o "$work/" ./cmd/node-agent ./cmd/job-request
podman --remote=false pull --tls-verify=true -- "$reference"
OLLAMA_IMAGE="$reference" OLLAMA_VOLUME=ollama-models "$script_dir/provision-model.sh"

"$work/node-agent" -serve -socket "$work/control.sock" -allowlist "$work/allowlist.json" \
    -job-listen 127.0.0.1:0 -job-ollama-digest "$digest" > "$work/agent.log" 2>&1 &
agent_pid=$!

url=
for ((attempt = 0; attempt < 100; attempt++)); do
    url=$(sed -n 's/.*requester job API listening on \(http:\/\/127\.0\.0\.1:[0-9]*\)$/\1/p' "$work/agent.log")
    [[ -z $url ]] || break
    kill -0 "$agent_pid" 2>/dev/null || break
    sleep 0.1
done
[[ -n $url ]] || { echo "The provider job listener did not start." >&2; exit 1; }

"$work/job-request" -url "$url" -seconds 300 -num-predict 16 \
    -prompt 'Say hello in one short sentence.' > "$work/result.ndjson"
jq -es 'length == 2 and .[0].type == "accepted" and
    (.[0].lease_id as $lease | .[1] |
        .type == "result" and .lease_id == $lease and
        .outcome == "completed" and .exit_code == 0 and
        (.inference | .status == "ok" and .model == "tinyllama:1.1b" and
            .processor == "cpu" and (.response | test("\\S")) and
            .metrics.completion_tokens > 0))' "$work/result.ndjson" >/dev/null
cat "$work/result.ndjson"
