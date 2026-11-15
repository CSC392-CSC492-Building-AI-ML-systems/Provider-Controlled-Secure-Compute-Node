#!/usr/bin/env bash

# Builds the curated Ollama image from committed source, pushes it under its
# version tag, and prints the immutable digest and the allowlist entry that
# lets provider nodes run it.
#
# Usage: containers/ollama/publish-image.sh REPOSITORY
#   REPOSITORY   registry and repository without a tag, for example
#                ghcr.io/csc392-csc492-building-ai-ml-systems/provider-node-ollama
#   PLATFORMS    comma-separated target platforms (default linux/amd64)
#
# Log in to the registry first (`podman login ghcr.io`).

set -euo pipefail

script_dir=$(cd "$(dirname "$0")" && pwd)
platforms="${PLATFORMS:-linux/amd64}"

fail() {
    printf 'publish-image: %s\n' "$*" >&2
    exit 1
}

[[ $# == 1 ]] || fail "usage: $0 REPOSITORY"
repository=$1

# The node agent only accepts an explicit registry and an untagged, lowercase
# repository, so reject anything it would refuse later.
repository_pattern='^([a-z0-9]+([.-][a-z0-9]+)+)(:[0-9]+)?/[a-z0-9]+([._-][a-z0-9]+)*(/[a-z0-9]+([._-][a-z0-9]+)*)*$'
[[ $repository =~ $repository_pattern ]] ||
    fail "REPOSITORY must be lowercase, include a registry host and have no tag or digest: $repository"

command -v podman >/dev/null || fail "Podman is required"
command -v git >/dev/null || fail "git is required"

# Publish only committed source, so the digest can be traced to a commit.
cd "$script_dir"
[[ -z $(git status --porcelain -- .) ]] ||
    fail "containers/ollama has uncommitted or untracked changes; commit them first"
revision=$(git rev-parse HEAD)
# Fixing every timestamp to the commit time makes rebuilding the same commit
# produce the same layers.
timestamp=$(git log -1 --format=%ct HEAD)

version=$(sed -n 's/^LABEL org\.opencontainers\.image\.version="\(.*\)"$/\1/p' Containerfile)
[[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "could not read a version label from the Containerfile"
reference="${repository}:${version}"

if command -v skopeo >/dev/null && skopeo inspect --raw "docker://${reference}" >/dev/null 2>&1; then
    fail "${reference} already exists; bump the version label in the Containerfile instead of replacing it"
fi

work=$(mktemp -d)
trap 'rm -rf "$work"; podman --remote=false manifest rm "$reference" >/dev/null 2>&1 || true' EXIT

echo "Building ${reference} for ${platforms} from commit ${revision}."
podman --remote=false manifest rm "$reference" >/dev/null 2>&1 || true
podman --remote=false build \
    --platform "$platforms" \
    --manifest "$reference" \
    --timestamp "$timestamp" \
    --label "org.opencontainers.image.revision=${revision}" \
    --file Containerfile \
    .

echo "Pushing ${reference}."
podman --remote=false manifest push --all \
    --digestfile "$work/digest" \
    "$reference" "docker://${reference}"

digest=$(<"$work/digest")
[[ $digest =~ ^sha256:[a-f0-9]{64}$ ]] || fail "registry returned an unexpected digest: $digest"

cat <<EOF

Published ${reference}
Digest:    ${digest}
Pull by:   ${repository}@${digest}

Add this entry to the provider allowlist's "images" array:
{"digest": "${digest}", "image": "${repository}"}
EOF
