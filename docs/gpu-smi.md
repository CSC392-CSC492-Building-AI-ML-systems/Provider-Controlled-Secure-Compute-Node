# GPU SMI Capability Collector

`internal/capability/gpu_smi.go` collects NVIDIA GPU information by invoking
`nvidia-smi`. It provides two kinds of data:

- **Static capabilities:** GPU index, UUID, model name, total VRAM, PCI bus ID,
  compute capability, MIG status, NVIDIA driver version, and CUDA version.
- **Live metrics:** per-GPU VRAM used, VRAM free, utilization percentage, and
  temperature.

The collector does not call `nvidia-smi` directly. Instead, it uses an injected
`CommandRunner`, which makes command execution replaceable in tests and allows
the collector to be used on systems where the NVIDIA utility is unavailable.
Every command runs with a context timeout, defaulting to five seconds.

## Function responsibilities

### `NewGPUSMICollector`

Creates a `GPUSMICollector` with a command runner and timeout. If the supplied
timeout is zero or negative, it uses `defaultGPUSMICommandTimeout` (five
seconds).

### `Capabilities`

Builds the static capability report:

1. Runs plain `nvidia-smi` to obtain the driver and CUDA versions.
2. Parses those versions with `parseSMIVersions`.
3. Runs an `nvidia-smi --query-gpu` CSV query for GPU metadata.
4. Parses each GPU row with `parseGPUQuery`.
5. Returns a timestamped `dashboard.CapabilityReport`.

Any command or parsing failure stops the operation and is returned to the
caller with context about which query failed.

### `parseSMIVersions`

Scans the human-readable output from plain `nvidia-smi` for `Driver Version:`
and `CUDA Version:`. It returns the first value following each label and
returns an error if either version is missing.

### `parseGPUQuery`

Parses the seven-column CSV returned by the static GPU query. It converts the
GPU index and total VRAM to integers, parses the compute capability, converts
the MIG status to a boolean, and builds a `dashboard.GPU` for each row.

An empty result is returned as a non-nil empty slice so it serializes as `[]`
rather than `null`.

### `parseComputeCapability`

Splits a compute capability such as `8.6` into its integer major and minor
components. It rejects values that do not contain a dot or whose components
are not valid integers.

### `Metrics`

Runs an `nvidia-smi --query-gpu` CSV query for UUID, used/free VRAM,
utilization, and temperature. It parses the rows with `parseMetricsQuery` and
returns them in a timestamped `dashboard.Metrics` value.

### `parseMetricsQuery`

Parses the five-column live metrics CSV. The UUID identifies the GPU, while
the remaining four columns are parsed as `float64` values and stored in
`dashboard.GPUMetrics`, preserving decimal precision. Malformed CSV or
non-numeric metric values produce an error. As with static capabilities, an
empty result is returned as a non-nil empty slice.

### `run`

Executes `nvidia-smi` through the configured `CommandRunner`. It creates a
child context with the collector's timeout, passes through the command
arguments, and returns the command's combined output. It reports an error when
no runner has been configured.

