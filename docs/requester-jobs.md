# Submit a job to a provider node

The requester API is an opt-in listener on `node-agent -serve`. It shares
the lease manager with `gpu-ctl` and the dashboard, including the image
allowlist, one-job limit, drain, reclaim, and lease expiry. A standalone node
can serve requests without connecting to a coordinator.

## Start the node

Build the provider and test client with Go 1.23.4 or later. The provider also
needs [rootless Podman](podman-execution.md#host-prerequisites).

```sh
go build -o /tmp/provider-jobs/ ./cmd/node-agent ./cmd/job-request ./cmd/gpu-ctl
export NODE_JOB_TOKEN="$(openssl rand -hex 32)"
/tmp/provider-jobs/node-agent -serve -allowlist /path/to/allowlist.json \
  -job-listen 127.0.0.1:8081
```

Set the same `NODE_JOB_TOKEN` in the requester process. Use a securely
generated token and share it with trusted requesters through a private channel.
Every holder can submit jobs under the same provider policy. Restart the node
with a new token to revoke the old one. The API does not expose local provider
controls, and its token is separate from the outbound `NODE_API_KEY`.

Plain HTTP is permitted on literal loopback addresses. For access from another
machine, use an SSH tunnel to loopback or a TLS listener with a certificate
trusted by the requester and valid for its provider hostname.

```sh
/tmp/provider-jobs/node-agent -serve -allowlist /path/to/allowlist.json \
  -job-listen 0.0.0.0:8443 \
  -job-tls-cert /path/to/fullchain.pem -job-tls-key /path/to/key.pem
```

Use `-url https://provider.example:8443` on the client in this case.
Prefer binding the provider's private interface for deployments on a private
network.

## Submit an approved image

Add the exact digest and repository to the provider's [allowlist](allowlist.md).
For a local test, you can use the public image `quay.io/podman/hello` and
get its pinned digest with these commands:

```sh
podman pull quay.io/podman/hello:latest
reference=$(podman image inspect quay.io/podman/hello:latest --format '{{index .RepoDigests 0}}')
digest=${reference##*@}
printf '{"images":[{"digest":"%s","image":"quay.io/podman/hello"}]}\n' \
  "$digest" > /tmp/hello-allowlist.json
```

Point the provider at `/tmp/hello-allowlist.json` when starting it, then use
the client in a second terminal with the same token and digest.

```sh
/tmp/provider-jobs/job-request -digest "$digest" -seconds 60
```

The client prints an `accepted` line followed by a terminal `result` line
with the same `lease_id`. A generic result includes the exit code when
available. The image runs its default command with the provider's existing
rootless isolation and resource limits. The API has no fields for commands,
arguments, mounts, devices, registries, or container logs.

## Submit an Ollama prompt

On the provider, [build and publish the curated image from this revision and
provision the approved model](../containers/ollama/README.md). Add the new
image's pinned digest to the allowlist and add `-job-ollama-digest "$ollama_digest"` to the node
command. The configured image and its bundled model receive all prompt jobs.
Add `-job-require-gpu` on a provider configured for NVIDIA CDI to require
GPU inference. Without that flag, the node does not attach the GPU.

```sh
/tmp/provider-jobs/job-request -prompt 'Explain what a GPU is in one sentence.' \
  -num-predict 32 -seconds 300
```

Pass `-prompt-file /path/to/prompt` or `-prompt-file -` to avoid putting the
prompt in the command line. The terminal result contains the
[Ollama response](../containers/ollama/JOB_CONTRACT.md#response), including
generated text and reported processor on success or structured error details
when the container provides them. The provider's normal lease reports include
job metadata and omit request and response text.

## HTTP contract

Send `POST /v1/jobs` with `Authorization: Bearer <token>` and
`Content-Type: application/json`. Supply exactly one of `image_digest`
or `inference` and optionally a lease length in seconds, from 1 through
3,600. An omitted or zero duration uses 1,800 seconds. The lease includes
setup time. The request body is limited to 64 KiB and the [inference
contract](../containers/ollama/JOB_CONTRACT.md#request) limits the prompt
and generation options.

```json
{"image_digest":"sha256:<64 lowercase hex characters>","seconds":60}
```

```json
{"inference":{"prompt":"What is a GPU?","options":{"num_predict":32}},"seconds":300}
```

Replies are newline-delimited JSON (`application/x-ndjson`). HTTP 200
starts with an acceptance emitted after the policy check and reservation,
before the image is pulled. Keep the response open to read the final line.

```json
{"type":"accepted","lease_id":"push-..."}
{"type":"result","lease_id":"push-...","outcome":"completed","exit_code":0}
```

Rejections return an HTTP error status and a single event. Common responses
are 401 for invalid credentials, 400 for invalid fields, 403 for a denied
digest or disabled inference, and 409 for a busy, draining, or disconnected
node. The requester command exits nonzero on rejection, an incomplete
response, or a terminal outcome other than `completed`.

The connection is the result delivery channel. Disconnecting makes the node
cancel work once it detects the disconnect. The node has no requester result
history or automatic retry, so an interrupted connection leaves the requester
without a verified outcome.
