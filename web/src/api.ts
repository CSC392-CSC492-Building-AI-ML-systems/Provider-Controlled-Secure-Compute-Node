// Typed client for the node's dashboard API. Shapes mirror docs/dashboard-api.md;
// change that document first, then these types.

export type AgentState = 'IDLE' | 'BUSY' | 'DRAIN' | 'RECLAIM'
export type CoordinatorStatus = 'UNREGISTERED' | 'ACTIVE' | 'STALE' | 'DRAINING' | 'DRAINED'

export interface NodeStatus {
  provider_id: string
  state: AgentState
  lease_id?: string
  coordinator_status: CoordinatorStatus
  last_heartbeat_at?: string
  mesh_ip?: string
}

export interface GPU {
  index: number
  uuid: string
  name: string
  vram_total_mb: number
  pci_bus_id: string
  compute_capability: { major: number; minor: number }
  mig_enabled: boolean
}

export interface CapabilityReport {
  schema_version: string
  collected_at: string
  gpus: GPU[]
  driver: { version: string; cuda_version: string }
  runtimes: {
    podman: { installed: boolean; version?: string; rootless: boolean }
    cdi: { available: boolean; devices: string[] }
  }
  host: {
    cpu_model: string
    cpu_cores: number
    ram_total_mb: number
    os: string
    kernel: string
    arch: string
  }
  probe_errors: { probe: string; error: string }[]
}

export interface GPUMetrics {
  uuid: string
  vram_used_mb: number
  vram_free_mb: number
  utilization_pct: number
  temperature_c: number
}

export interface Metrics {
  collected_at: string
  gpus: GPUMetrics[]
}

export type ProbeStatus = 'pass' | 'warn' | 'fail' | 'not_implemented' | 'not_run'

export interface ProbeRow {
  id: string
  name: string
  requirement: string
  layer: string
  owner: string
  status: ProbeStatus
  message: string
  checked_at?: string
}

export type EventLevel = 'info' | 'warn' | 'error'

export interface NodeEvent {
  time: string
  level: EventLevel
  source: string
  message: string
}

export class ApiError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function request<T>(method: 'GET' | 'POST', path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new ApiError(res.status, data.error ?? res.statusText)
  }
  return data as T
}

export const api = {
  status: () => request<NodeStatus>('GET', '/api/status'),
  capabilities: () => request<CapabilityReport>('GET', '/api/capabilities'),
  metrics: () => request<Metrics>('GET', '/api/metrics'),
  probes: () => request<{ probes: ProbeRow[] }>('GET', '/api/probes').then((r) => r.probes),
  runAllProbes: () => request<{ probes: ProbeRow[] }>('POST', '/api/probes/run').then((r) => r.probes),
  runProbe: (id: string) => request<ProbeRow>('POST', `/api/probes/${encodeURIComponent(id)}/run`),
  drain: (gracePeriodSeconds?: number) =>
    request<NodeStatus>(
      'POST',
      '/api/control/drain',
      gracePeriodSeconds === undefined ? undefined : { grace_period_seconds: gracePeriodSeconds },
    ),
  reclaim: () => request<NodeStatus>('POST', '/api/control/reclaim'),
}

// subscribeEvents streams the diagnostic log. It returns a function that closes the stream.
export function subscribeEvents(onEvent: (e: NodeEvent) => void): () => void {
  const source = new EventSource('/api/events')
  source.onmessage = (msg) => onEvent(JSON.parse(msg.data) as NodeEvent)
  return () => source.close()
}
