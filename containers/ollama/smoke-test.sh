#!/usr/bin/env bash

# End-to-end test for the curated Ollama image on a rootless Podman host.
#
# It builds the image, provisions the approved model, then checks:
#   serve mode: HTTP readiness, /api/tags lists only the approved model,
#               /api/generate returns text, and `podman stop` is clean;
#   job mode:   a valid request succeeds, malformed and model-override
#               requests are rejected with exit code 2.
# Containers run with the same restrictions the node agent applies.
#
# Usage: containers/ollama/smoke-test.sh
#   SKIP_BUILD=1     use an existing image instead of building it
#   OLLAMA_IMAGE     image to build and test (default localhost/provider-node-ollama:0.3.0)
#   OLLAMA_VOLUME    model volume (default ollama-models)
#   SMOKE_PORT       host port for serve mode (default 11435)
#   SMOKE_GPU=1      pass the NVIDIA GPU through CDI and fail if inference
#                    does not run entirely on the GPU
#
# Exits nonzero as soon as any check fails.

set -euo pipefail

script_dir=$(cd "$(dirname "$0")" && pwd)
image="${OLLAMA_IMAGE:-localhost/provider-node-ollama:0.3.0}"
volume="${OLLAMA_VOLUME:-ollama-models}"
port="${SMOKE_PORT:-11435}"
model=tinyllama:1.1b
name="ollama-smoke-$$"
work=""

step() { printf '\n==> %s\n' "$*"; }
fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

for tool in podman curl jq; do
    command -v "$tool" >/dev/null || fail "$tool is required"
done
[[ $(podman --remote=false info --format '{{.Host.Security.Rootless}}') == true ]] ||
    fail "rootless Podman is required"

cleanup() {
    podman --remote=false rm --force --ignore --time 0 "$name" >/dev/null 2>&1 || true
    if [[ -n $work ]]; then
        # Files written by the container belong to a subordinate UID.
        podman --remote=false unshare rm -rf "$work" || rm -rf "$work" || true
    fi
}
trap cleanup EXIT

# Restrictions matching internal/containerengine, plus what this image needs:
# the model volume and enough memory for TinyLlama.
restricted=(
    --userns=nomap --user=65534:65534
    --cap-drop=ALL --security-opt=no-new-privileges
    --read-only --read-only-tmpfs=false
    --tmpfs=/tmp:rw,noexec,nosuid,nodev,size=64m,mode=1777
    --pids-limit=128 --memory=2g --memory-swap=2g --cpus=2
    --volume="${volume}:/models:ro,Z"
)

gpu=${SMOKE_GPU:-0}
if [[ $gpu == 1 ]]; then
    # Confirms Podman can resolve the CDI device before the real tests run.
    podman --remote=false run --rm --device=nvidia.com/gpu=all --entrypoint=/bin/true "$image" ||
        fail "SMOKE_GPU=1 needs the NVIDIA Container Toolkit and CDI specs; see README.md"
    # OLLAMA_JOB_REQUIRE_GPU makes job mode fail with exit code 32 on CPU fallback.
    restricted+=(--device=nvidia.com/gpu=all --env=OLLAMA_JOB_REQUIRE_GPU=1)
fi

if [[ ${SKIP_BUILD:-0} != 1 ]]; then
    step "Building $image"
    podman --remote=false build --tag "$image" --file "$script_dir/Containerfile" "$script_dir"
fi

step "Provisioning the approved model into volume $volume"
OLLAMA_IMAGE="$image" OLLAMA_VOLUME="$volume" "$script_dir/provision-model.sh"

work=$(mktemp -d /tmp/ollama-smoke.XXXXXX)
# mktemp creates the directory as 0700; the container user must traverse it.
chmod 0755 "$work"

# --- serve mode -------------------------------------------------------------

step "Starting serve mode on 127.0.0.1:$port"
podman --remote=false run --detach --name "$name" \
    "${restricted[@]}" \
    --publish "127.0.0.1:${port}:11434" \
    "$image" serve >/dev/null

# Poll Ollama directly. Podman's default OCI build omits Docker HEALTHCHECK metadata.
ready=0
deadline=$((SECONDS + 120))
while (( SECONDS < deadline )); do
    if curl --fail --silent --max-time 1 --output /dev/null "http://127.0.0.1:${port}/api/version"; then
        ready=1
        break
    fi
    [[ $(podman --remote=false inspect --format '{{.State.Running}}' "$name") == true ]] || {
        podman --remote=false logs "$name" >&2 || true
        fail "serve container exited during startup"
    }
    sleep 2
done
[[ $ready == 1 ]] || fail "Ollama did not become ready within 120 seconds"
echo "Ollama HTTP API is ready."

tags=$(curl --fail --silent --show-error "http://127.0.0.1:${port}/api/tags")
[[ $(jq -r '[.models[].name] | join(",")' <<<"$tags") == "$model" ]] ||
    fail "/api/tags should list only $model, got: $tags"
echo "/api/tags lists only $model."

generated=$(curl --fail --silent --show-error --max-time 300 \
    "http://127.0.0.1:${port}/api/generate" \
    --data "{\"model\":\"$model\",\"prompt\":\"Say hello in one short sentence.\",\"stream\":false,\"options\":{\"num_predict\":32,\"seed\":1}}")
[[ -n $(jq -r '.response // "" | gsub("^\\s+|\\s+$"; "")' <<<"$generated") ]] ||
    fail "/api/generate returned an empty response: $generated"
echo "/api/generate returned text."

if [[ $gpu == 1 ]]; then
    loaded=$(curl --fail --silent --show-error "http://127.0.0.1:${port}/api/ps")
    jq -e --arg model "$model" \
        '[.models[] | select(.name == $model) | .size_vram >= .size and .size > 0] == [true]' \
        <<<"$loaded" >/dev/null || fail "model is not fully loaded on the GPU: $loaded"
    echo "Model is loaded on the GPU."
fi

step "Stopping serve mode"
podman --remote=false stop --time 10 "$name" >/dev/null
code=$(podman --remote=false inspect --format '{{.State.ExitCode}}' "$name")
# 137 means Podman had to kill Ollama after the stop timeout.
[[ $code != 137 ]] || fail "Ollama did not shut down within 10 seconds of SIGTERM"
echo "Ollama stopped cleanly (exit code $code)."
podman --remote=false rm "$name" >/dev/null

# --- job mode ---------------------------------------------------------------

# run_job NAME REQUEST: runs one job and sets job_exit and job_output.
run_job() {
    local dir="$work/$1"
    mkdir -p "$dir/input" "$dir/output"
    printf '%s\n' "$2" >"$dir/input/request.json"
    chmod 0755 "$dir" "$dir/input"
    chmod 0644 "$dir/input/request.json"
    chmod 0777 "$dir/output"

    set +e
    podman --remote=false run --rm \
        "${restricted[@]}" \
        --network=none \
        --volume="$dir/input:/job/input:ro,Z" \
        --volume="$dir/output:/job/output:rw,Z" \
        "$image"
    job_exit=$?
    set -e

    job_output="$dir/output/response.json"
    [[ -f $job_output ]] || fail "$1: job wrote no response.json (exit code $job_exit)"
}

step "Job mode: valid request"
run_job valid '{"prompt":"Say hello in one short sentence.","options":{"num_predict":32,"seed":1}}'
[[ $job_exit == 0 ]] || fail "valid job exited with $job_exit: $(cat "$job_output")"
jq -e --arg model "$model" \
    '.status == "ok" and .model == $model and (.response | test("\\S")) and (.processor | type == "string")' \
    "$job_output" >/dev/null || fail "unexpected valid-job response: $(cat "$job_output")"
processor=$(jq -r .processor "$job_output")
if [[ $gpu == 1 && $processor != gpu ]]; then
    fail "job ran on processor $processor, want gpu"
fi
echo "Job succeeded on processor $processor."

step "Job mode: malformed request"
run_job malformed '{"prompt": '
[[ $job_exit == 2 ]] || fail "malformed job exited with $job_exit, want 2"
jq -e '.status == "error" and .error.code == "invalid_request"' "$job_output" >/dev/null ||
    fail "unexpected malformed-job response: $(cat "$job_output")"
echo "Malformed request rejected."

step "Job mode: request that tries to choose a model"
run_job override '{"prompt":"hi","model":"llama3"}'
[[ $job_exit == 2 ]] || fail "model-override job exited with $job_exit, want 2"
jq -e '.error.code == "invalid_request"' "$job_output" >/dev/null ||
    fail "unexpected model-override response: $(cat "$job_output")"
echo "Model override rejected."

printf '\nPASS: the curated Ollama image served, stopped cleanly and ran inference jobs.\n'
