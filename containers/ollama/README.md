# Curated Ollama Inference Container

This directory contains the curated inference image for the provider node.
By default the image runs one inference job against one approved model and
exits; it can also run the Ollama HTTP API for development. The model is read
from a Podman volume.

The base image is Ollama `0.40.6`, pinned to its immutable multi-architecture
OCI digest. This prevents a later tag update from silently changing the
software that runs on a provider node.

## Status

| Area | State |
| --- | --- |
| Job runner (`jobrunner/`) | Unit-tested with `go test` against a fake Ollama server |
| Image build, provisioning, job and serve modes | Verified on Linux with rootless Podman and CPU inference |
| GPU inference | Pending verification on an NVIDIA provider with the Container Toolkit |
| Image distribution | Digest-pinned push and pull verified with a local TLS registry |
| Node-agent integration | Verified with a requester prompt and structured CPU result through the running node agent (see [the requester guide](../../docs/requester-jobs.md)) |

The first step on a Linux host with rootless Podman is to run
[`smoke-test.sh`](#smoke-test) and fix anything it reports. Podman on macOS
(`podman machine`) can run the CPU checks too.

## Approved model

The approved development model is **TinyLlama 1.1B**:

| Property | Value |
| --- | --- |
| Ollama identifier | `tinyllama:1.1b` |
| Format | GGUF, `Q4_0` quantization |
| Parameters | 1.1 billion |
| Context length | 2,048 tokens |
| Weight digest | `sha256:2af3b81862c6be03c769683af18efdadb2c33f60ff32ab6f83e42c043d6c7816` |
| Download size | 638 MB (637,699,456 bytes) |
| Disk space to reserve | 1 GB |
| Memory to reserve | 2 GB minimum, 4 GB recommended for CPU inference |
| License | Apache License 2.0 |

TinyLlama is a compact Llama-architecture model published by the TinyLlama
project. Its project license is Apache License 2.0:

- Project: <https://github.com/jzhang38/TinyLlama>
- License: <https://github.com/jzhang38/TinyLlama/blob/main/LICENSE>
- Ollama library entry: <https://ollama.com/library/tinyllama:1.1b>

The model is small enough for a CPU smoke test. It is a development model, not
the final production model. Replacing it requires editing
`approved-model.env`, rebuilding the image, and provisioning the new model.
That rebuild changes the image digest.

The model is stored in the named Podman volume `ollama-models`, not copied
into the image. The volume is mounted read-only while the service runs, so a
request cannot save a different model into it.

`approved-model.env` pins the model's entire manifest: every entry's role
(media type), digest and size, in order. At startup the job runner
(`ollama-job verify-model`) decodes the installed manifest as JSON, the same
way Ollama reads it, and requires its entries to match exactly. Duplicate or
unknown JSON keys are rejected, so no other parser can read the manifest
differently. It then hashes every referenced file (config, weights, prompt
template, system prompt and parameters) with SHA-256. It rejects a missing
model, an added, removed, reordered or reassigned layer, a size or digest
mismatch, and any additional model manifest, including a symlinked one.

## Current scope

The image has two modes, selected by the container command:

- **`job` (default)** runs one inference job and exits. The request is read
  from `/job/input/request.json` and the result is written to
  `/job/output/response.json`. Ollama listens only on the container's
  loopback interface, so the container needs no network. This is the mode the
  node agent uses. The request and response formats, limits and exit codes
  are defined in [JOB_CONTRACT.md](JOB_CONTRACT.md).
- **`serve`** runs the Ollama HTTP API on port `11434` until it is stopped.
  It is meant for local development and manual testing.

Both modes verify the provisioned TinyLlama manifest and blob digests before
starting Ollama. Job mode exposes only the verified model files to Ollama.
The rootless Podman smoke test checks startup through the HTTP API.

The image is designed to run the way the node agent runs workloads: as
UID/GID `65534:65534`, with a read-only root filesystem, all capabilities
dropped and no network. `HOME` points at `/tmp`, Ollama does not prune the
read-only model volume, and provisioning makes the model files readable by
that user.

GPU inference is opt-in: pass the GPU with a CDI device (see
[GPU inference](#gpu-inference)). Without one, Ollama runs on the CPU. The
model is not downloaded during `podman build`.

## Requirements

- Podman
- `curl`, for the host-side readiness check in `serve` mode
- Internet access while building the image and provisioning the model
- About 1 GB of free disk space and 2 GB of available memory

Podman on macOS runs containers inside a Linux virtual machine. This image can
be built and CPU-tested there, but GPU passthrough must be tested on the target
Linux provider node.

## Build

Run from the repository root:

```sh
podman build \
  --tag localhost/provider-node-ollama:0.3.0 \
  --file containers/ollama/Containerfile \
  containers/ollama
```

The build compiles the job runner in `jobrunner/` with a digest-pinned Go
image, then copies the static binary into the Ollama image.

## Provision the approved model

The provisioning command accepts no model name. It always installs the model
pinned in the image and writes the pinned manifest after the download. The
provisioning container checks every pinned digest and runs with all Linux
capabilities dropped and `no-new-privileges`. Job mode builds its temporary
model store from that manifest and those verified blobs.

```sh
containers/ollama/provision-model.sh
```

This creates the `ollama-models` volume if needed and downloads about 638 MB.
If the pinned model is already present, the command leaves it unchanged
except for making it readable by the inference user. If a different or
modified model is already present, it stops without downloading anything. If
a new download fails partway or fails verification, its files are removed so
the next provisioning run can try again.

## Run one inference job

Create a job directory. The container user must be able to read the request
and write to the output directory:

```sh
job=$(mktemp -d)
mkdir "$job/input" "$job/output"
printf '%s\n' '{"prompt": "Explain what a GPU is in one sentence."}' \
  > "$job/input/request.json"
chmod 0755 "$job" "$job/input"
chmod 0644 "$job/input/request.json"
chmod 0777 "$job/output"
```

Run the job with the same restrictions the node agent uses:

```sh
podman run --rm \
  --user 65534:65534 \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --network none \
  --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,nodev,size=64m,mode=1777 \
  --memory 2g \
  --pids-limit 128 \
  --volume ollama-models:/models:ro,Z \
  --volume "$job/input:/job/input:ro,Z" \
  --volume "$job/output:/job/output:rw,Z" \
  localhost/provider-node-ollama:0.3.0
echo "exit code: $?"
cat "$job/output/response.json"
```

The exit code and `response.json` report the outcome; see
[JOB_CONTRACT.md](JOB_CONTRACT.md). Delete the job directory afterward, since
it contains the prompt and the response:

```sh
rm -rf "$job"
```

## Run the API server (development)

```sh
podman run --rm \
  --name provider-node-ollama \
  --user 65534:65534 \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,nodev,size=64m,mode=1777 \
  --publish 127.0.0.1:11434:11434 \
  --volume ollama-models:/models:ro,Z \
  localhost/provider-node-ollama:0.3.0 serve
```

Binding to `127.0.0.1` prevents the API from being exposed on every host
network interface. The read-only model mount prevents the running container
from installing another model.

The container exits before serving requests when the approved model is missing
or does not match the pinned digest.

### Verify readiness

In another terminal:

```sh
curl --fail --silent --show-error \
  http://127.0.0.1:11434/api/tags
```

The response should contain `tinyllama:1.1b` and no other model.

### Stop

Press `Ctrl+C` in the terminal running the container, or run:

```sh
podman stop provider-node-ollama
```

The entrypoint uses `exec`, so Ollama receives the stop signal directly and
can shut down cleanly. The model remains in the `ollama-models` volume for the
next run.

## GPU inference

Only NVIDIA GPUs are supported. The Ollama base image already contains the
CUDA libraries; the host provides the driver and exposes the GPU to rootless
Podman through the Container Device Interface (CDI). This has to be set up
and tested on the Linux provider node itself. A Mac cannot pass its GPU to a
Podman container.

### Host setup (Ubuntu, once, as an administrator)

1. Install the NVIDIA driver and confirm `nvidia-smi` works on the host.
2. Install the NVIDIA Container Toolkit by following NVIDIA's instructions
   for adding its apt repository, then:

   ```sh
   sudo apt-get install -y nvidia-container-toolkit
   ```

3. Generate the CDI specification and list the devices it defines:

   ```sh
   sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml
   nvidia-ctk cdi list
   ```

   The list should include `nvidia.com/gpu=0` and `nvidia.com/gpu=all`.
   Regenerate the file after every driver upgrade.

Podman 4.1 or newer reads `/etc/cdi` automatically. No change to the
container's capabilities or privileges is needed.

### Run with a GPU

Pass only the GPU the job is assigned, and set `OLLAMA_JOB_REQUIRE_GPU=1` so
the job fails with exit code 32 (`gpu_unavailable`) instead of quietly
running on the CPU:

```sh
podman run --rm \
  --device nvidia.com/gpu=0 \
  --env OLLAMA_JOB_REQUIRE_GPU=1 \
  ...the other options from "Run one inference job"... \
  localhost/provider-node-ollama:0.3.0
```

Without `OLLAMA_JOB_REQUIRE_GPU`, the job still reports where it ran in the
`processor` field of `response.json`: `gpu`, `cpu`, `partial` or `unknown`.

To run the smoke test with the GPU:

```sh
SMOKE_GPU=1 containers/ollama/smoke-test.sh
```

### Troubleshooting

| Symptom | Likely cause and fix |
| --- | --- |
| `unresolvable CDI devices nvidia.com/gpu=0` | The CDI spec is missing or stale. Rerun `nvidia-ctk cdi generate` and check `nvidia-ctk cdi list`. |
| `processor` is `cpu`, or exit code 32 | Ollama could not use the GPU. Check the container log for its GPU discovery messages, and check that `nvidia-smi` works on the host. |
| `processor` is `partial` | The model did not fit in free GPU memory. Check other processes in `nvidia-smi`. |
| `nvidia-smi` fails on the host after an update | The driver and kernel module versions differ. Reboot, then regenerate the CDI spec. |
| Permission denied on `/dev/nvidia*` | The device nodes must be readable and writable by all users (mode `0666`), which is the driver's default. |

## Smoke test

On a Linux host with rootless Podman, `curl` and `jq`, run from the
repository root:

```sh
containers/ollama/smoke-test.sh
```

It builds the image, provisions the model, and then checks both modes using
the node agent's container restrictions:

- `serve` mode answers its HTTP readiness probe, `/api/tags` lists only
  `tinyllama:1.1b`, `/api/generate` returns text, and `podman stop` shuts
  Ollama down without a forced kill;
- `job` mode completes a valid request with a nonempty response, and rejects a
  malformed request and a request that tries to choose a model.

It removes its containers and job files and exits nonzero on the first failed
check. Set `SKIP_BUILD=1` to test an image you have already built.

## Publish and allowlist the image

Provider nodes only run images listed by digest in their allowlist, and the
node agent pulls them from a registry with TLS verification. A local
`localhost/...` build cannot be used by the node agent.

The suggested registry is the GitHub Container Registry (`ghcr.io`) under the
project's GitHub organization. Log in with a personal access token that has
the `write:packages` scope:

```sh
podman login ghcr.io
```

Then publish from a clean checkout:

```sh
containers/ollama/publish-image.sh \
  ghcr.io/csc392-csc492-building-ai-ml-systems/provider-node-ollama
```

The script:

1. refuses to run if `containers/ollama` has uncommitted changes, so every
   published digest corresponds to a commit (recorded in the
   `org.opencontainers.image.revision` label);
2. builds for `linux/amd64` (set `PLATFORMS=linux/amd64,linux/arm64` for
   both; building for another architecture than the host's needs
   `qemu-user-static`);
3. fixes file timestamps to the commit time, so rebuilding the same commit
   produces the same layers;
4. tags the image with the version from the Containerfile's
   `org.opencontainers.image.version` label and refuses to replace an
   existing version when `skopeo` is installed;
5. pushes the image and prints its digest and the allowlist entry:

   ```json
   {"digest": "sha256:<64 hex characters>", "image": "ghcr.io/csc392-csc492-building-ai-ml-systems/provider-node-ollama"}
   ```

Add that entry to the `images` array of the provider's allowlist file. New
GitHub packages are private: either make the package public or log in on each
provider node with a read-only token.

### Updating the image

A digest is a hash of the image's content, so any change (a script, the
approved model, the base image) gives a new digest. Nodes keep refusing the
new image until a provider allowlists it. To release a change:

1. Commit the change and bump `org.opencontainers.image.version` in the
   Containerfile.
2. Run the smoke test against the new build.
3. Run `publish-image.sh` and add the printed entry to each provider's
   allowlist.
4. Remove the old digest from the allowlist once no leases use it.

## Develop the job runner

The job runner is a small Go program with no dependencies outside the
standard library. Its tests use a fake Ollama server, so they run without
Podman or a model:

```sh
cd containers/ollama/jobrunner
go test ./...
```
