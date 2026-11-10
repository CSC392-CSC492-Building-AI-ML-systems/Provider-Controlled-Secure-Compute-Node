#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
command -v podman >/dev/null || { echo 'Install Podman first: sudo apt-get update && sudo apt-get install -y podman uidmap slirp4netns fuse-overlayfs' >&2; exit 1; }
if command -v go >/dev/null; then go_tool=go; elif [[ -x /tmp/go/bin/go ]]; then go_tool=/tmp/go/bin/go; else echo 'Install Go 1.23.4 or newer first.' >&2; exit 1; fi
[[ $(podman --remote=false info --format '{{.Host.Security.Rootless}}') == true ]] || { echo 'Rootless Podman is required.' >&2; exit 1; }
smoke_dir=$(mktemp -d /tmp/node-podman-smoke.XXXXXX)
# Keep artifacts for inspecting the offer and result after the run.
echo "Smoke-test files: $smoke_dir"
image=quay.io/podman/hello
podman --remote=false pull --tls-verify=true "$image:latest"
image_ref=$(podman --remote=false image inspect "$image:latest" --format '{{index .RepoDigests 0}}')
image_digest=${image_ref##*@}
[[ $image_digest =~ ^sha256:[a-f0-9]{64}$ ]] || { echo 'Could not resolve image digest.' >&2; exit 1; }
expiry=$(date -u -d '+5 minutes' +%Y-%m-%dT%H:%M:%SZ)
printf '{"images":[{"digest":"%s","image":"%s"}]}\n' "$image_digest" "$image" > "$smoke_dir/allowlist.json"
printf '{"ID":"podman-smoke","ImageDigest":"%s","ExpiresAt":"%s"}\n' "$image_digest" "$expiry" > "$smoke_dir/offer.json"
GOCACHE="${GOCACHE:-/tmp/compute-node-go-cache}" "$go_tool" build -o "$smoke_dir/node-agent" ./cmd/node-agent
if ! "$smoke_dir/node-agent" -show-output -allowlist "$smoke_dir/allowlist.json" < "$smoke_dir/offer.json" > "$smoke_dir/events.jsonl" 2> "$smoke_dir/workload.log"; then
 cat "$smoke_dir/workload.log" "$smoke_dir/events.jsonl" >&2
 exit 1
fi
cat "$smoke_dir/workload.log" "$smoke_dir/events.jsonl"
grep -q 'Hello Podman World' "$smoke_dir/workload.log"
grep -q '"outcome":"completed"' "$smoke_dir/events.jsonl"
echo 'PASS: the lender ran the approved image in rootless Podman and collected its greeting.'
