# Capability Detection Design Choices

This document records the implementation decisions for GPU, runtime, host,
and capability-probe detection.

## GPU collection

### Use `nvidia-smi` through an injected runner

`GPUSMICollector` receives a `CommandRunner` instead of calling
`exec.Command` directly. This keeps command execution replaceable in tests and
allows the collector to report command failures cleanly when NVIDIA tools are
not installed.

Every `nvidia-smi` invocation runs with a five-second context timeout. The
timeout applies independently to the plain header query, the static GPU CSV
query, and the live metrics CSV query.

### Separate static capabilities from live metrics

Static information is collected by `Capabilities` and includes GPU identity,
model, total VRAM, PCI bus ID, compute capability, MIG status, driver version,
and CUDA version. Live usage is collected by `Metrics` and includes used/free
VRAM, utilization, and temperature.

The live metrics response is matched to static GPU information by UUID rather
than by array position.

### Parse CSV instead of human-readable GPU rows

The collector requests CSV output with `--format=csv,noheader,nounits`. This
avoids depending on column spacing or localized human-readable output. GPU
names are still handled correctly when they contain commas because parsing uses
the CSV format.

### Represent unavailable numeric metrics as zero

`nvidia-smi` can return `[N/A]` or `[Not Supported]` for a metric. The API
currently exposes these fields as numeric `float64` values, so the parser maps
either status to `0` instead of returning a parsing error.

Before this handling was added, `strconv.ParseFloat` was called directly and
these values caused the entire metrics request to fail. Normal integer and
decimal values continue to be parsed unchanged.

For example:

```text
[N/A]            -> 0
[Not Supported]  -> 0
41.25            -> 41.25
```

### Treat MIG `N/A` as disabled

The MIG field is represented as a boolean. Only the case-insensitive value
`enabled` becomes `true`; `[N/A]`, `disabled`, and other values become
`false`. This matches GPUs such as the RTX 4080, which report MIG as `[N/A]`.

### Preserve empty GPU arrays

An empty query result is represented as a non-nil empty slice. JSON therefore
contains `"gpus": []` rather than `"gpus": null`.

## Runtime detection

Podman is queried with `podman info --format json`; the detector extracts the
version, rootless status, and OCI runtime. A missing Podman executable is an
ordinary unavailable-runtime result, not a detector crash.

CDI devices are queried with `nvidia-ctk cdi list`. If `nvidia-ctk` is not
installed, the detector falls back to checking for CDI spec files in
`/etc/cdi` and `/var/run/cdi`.

## Host detection

Host information is read through `fs.FS` so callers can provide an in-memory
or fixture filesystem. Linux-style files provide the CPU model and count,
memory total, OS release, and kernel version. The process architecture comes
from Go's `runtime.GOARCH`.

## Capability probes

The three probes implement the shared dashboard probe interface:

- `capability_discovery` passes only when capability collection succeeds and
  reports no probe errors.
- `rootless_podman_cdi` passes only when Podman is installed and rootless and
  CDI is available.
- `model_mount` passes only when `/var/cache/models` exists and is readable as
  a directory.

Probe dependencies are injected where they affect external state, allowing
the probe logic to be evaluated independently of the host machine.
