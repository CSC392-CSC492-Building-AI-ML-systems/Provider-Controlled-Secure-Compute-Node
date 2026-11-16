# GPU Allocation and Sharing

This document records future work for assigning only part of a provider's GPU
capacity to a container. It is intentionally separate from the current
rootless Podman runner implementation.

## Current behavior

GPU-required workloads currently receive the NVIDIA CDI device:

```text
nvidia.com/gpu=all
```

The runner also sets `OLLAMA_JOB_REQUIRE_GPU=1`, so the curated inference job
fails if it cannot actually use a GPU. CPU workloads do not receive a GPU
device.

This means the current GPU path is appropriate only when the provider is
willing to expose all CDI-visible GPUs to that workload.

## Future provider-controlled allocation

The provider should choose the device assignment from its own policy instead
of allowing a client or coordinator to submit arbitrary device names.

An eventual policy could map approved workload classes to specific CDI devices:

```text
workload A → nvidia.com/gpu=0
workload B → nvidia.com/gpu=1
```

The runner would then:

1. Select a device from the provider-owned policy.
2. Verify that the selected CDI device is available.
3. Pass only that device to Podman.
4. Reject the workload if the device is unavailable.
5. Release the assignment when the lease ends.

The coordinator may request GPU capability, but it must not choose an
arbitrary host device. The provider retains final control over allocation.

## MIG and stronger partitioning

Selecting one physical GPU does not guarantee a fixed share of its VRAM or
compute capacity. Workloads may still compete for resources on that GPU.

For stronger isolation, providers can use NVIDIA Multi-Instance GPU (MIG)
where supported. MIG creates separately addressable GPU instances that can be
assigned through CDI. The provider could then approve specific MIG devices
instead of exposing an entire physical GPU.

## Resource and scheduling considerations

Future allocation work should define:

- A provider-owned allowlist of approved GPU and MIG CDI devices.
- Whether a device may be shared concurrently.
- Per-device lease ownership and release behavior.
- GPU memory and compute utilization limits.
- What happens when a requested device is busy.
- How reclaim handles a device assignment.
- Capability reports that describe available GPU partitions.
- Tests proving that a workload cannot receive an unapproved device.

The container's CPU, memory, PID, and lifetime limits remain necessary. They do
not by themselves partition GPU VRAM or compute time.

## Related implementation

The current CDI integration is in
[`internal/containerengine/engine.go`](../internal/containerengine/engine.go).
The provider capability and CDI detection code is in
[`internal/capability/runtimes.go`](../internal/capability/runtimes.go).
