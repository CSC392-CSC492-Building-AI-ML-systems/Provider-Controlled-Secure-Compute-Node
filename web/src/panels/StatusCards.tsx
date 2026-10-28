import type { ReactNode } from 'react'
import { api } from '../api'
import { usePoll } from '../usePoll'

const gb = (mb: number) => (mb / 1024).toFixed(1)
const clampPercent = (value: number) => Math.max(0, Math.min(100, value))

export function StatusCards() {
  const status = usePoll(api.status, 2000)
  const caps = usePoll(api.capabilities, 30000)
  const metrics = usePoll(api.metrics, 2000)

  const gpus = caps.data?.gpus ?? []
  const liveByUUID = new Map((metrics.data?.gpus ?? []).map((metric) => [metric.uuid, metric]))
  const totalVRAM = gpus.reduce((total, gpu) => total + gpu.vram_total_mb, 0)
  const usedVRAM = gpus.reduce((total, gpu) => total + (liveByUUID.get(gpu.uuid)?.vram_used_mb ?? 0), 0)
  const hasLiveMetrics = gpus.some((gpu) => liveByUUID.has(gpu.uuid))
  const vramPercent = totalVRAM > 0 ? clampPercent((100 * usedVRAM) / totalVRAM) : 0
  const podman = caps.data?.runtimes.podman
  const cdi = caps.data?.runtimes.cdi
  const models = caps.data?.models ?? []

  return (
    <section className="cards" aria-label="Node status and capabilities">
      <Card title="Agent State" error={status.error}>
        <div className="value">{status.data?.state ?? '-'}</div>
        <div className="sub">{status.data?.lease_id ?? 'no active lease'}</div>
      </Card>

      <Card title="Mesh IP" error={status.error}>
        <div className="value">{status.data?.mesh_ip ?? '-'}</div>
        <div className="sub">coordinator: {status.data?.coordinator_status ?? '-'}</div>
      </Card>

      <Card title="VRAM Usage" error={caps.error} className="capability-card">
        {!caps.data ? (
          <LoadingValue />
        ) : gpus.length === 0 ? (
          <EmptyValue value="No GPU detected" detail="VRAM metrics are unavailable" />
        ) : metrics.error ? (
          <InlineError title="Metrics unavailable" message={metrics.error} />
        ) : !hasLiveMetrics ? (
          <EmptyValue value="Waiting for metrics" detail={`${gpus.length} GPU${gpus.length === 1 ? '' : 's'} detected`} />
        ) : (
          <>
            <div className="metric-row">
              <div>
                <span className="metric-value">{gb(usedVRAM)}</span>
                <span className="metric-unit"> / {gb(totalVRAM)} GiB</span>
              </div>
              <span className="metric-percent">{Math.round(vramPercent)}%</span>
            </div>
            <div className="bar" aria-label={`${Math.round(vramPercent)}% VRAM used`}>
              <div style={{ width: `${vramPercent}%` }} />
            </div>
            <div className="card-footer">
              <span>{gpus.length === 1 ? gpus[0].name : `${gpus.length} GPUs`}</span>
              <span>{gb(Math.max(totalVRAM - usedVRAM, 0))} GiB free</span>
            </div>
          </>
        )}
      </Card>

      <Card title="CDI Passthrough" error={caps.error} className="capability-card">
        {!cdi ? (
          <LoadingValue />
        ) : (
          <>
            <CapabilityState tone={cdi.available ? 'pass' : 'fail'}>
              {cdi.available ? 'Ready' : 'Unavailable'}
            </CapabilityState>
            <div className="sub capability-summary">
              {cdi.available
                ? `${cdi.devices.length} device${cdi.devices.length === 1 ? '' : 's'} exposed`
                : 'No CDI devices discovered'}
            </div>
            <div className="card-footer">
              <span>{cdi.devices.includes('nvidia.com/gpu=all') ? 'gpu=all configured' : 'per-device mapping'}</span>
              <span>CUDA {caps.data?.driver.cuda_version || '-'}</span>
            </div>
          </>
        )}
      </Card>

      <Card title="Container Sandbox" error={caps.error} className="capability-card">
        {!podman ? (
          <LoadingValue />
        ) : (
          <>
            <CapabilityState tone={podman.installed && podman.rootless ? 'pass' : 'fail'}>
              {!podman.installed ? 'Podman missing' : podman.rootless ? 'Rootless' : 'Rootful'}
            </CapabilityState>
            <div className="sub capability-summary">
              {!podman.installed
                ? 'Container sandbox is not available'
                : podman.rootless
                  ? 'Unprivileged container execution'
                  : 'Rootless mode is required'}
            </div>
            <div className="card-footer">
              <span>{podman.version ? `Podman ${podman.version}` : 'Version unavailable'}</span>
              <span>{podman.oci_runtime || '-'}</span>
            </div>
          </>
        )}
      </Card>

      <Card title="Model Mount" error={caps.error} className="capability-card">
        {!caps.data ? (
          <LoadingValue />
        ) : models.length === 0 ? (
          <EmptyValue value="No models mounted" detail="/var/cache/models is empty or unavailable" />
        ) : (
          <>
            <CapabilityState tone="pass">
              {models.length} model{models.length === 1 ? '' : 's'} verified
            </CapabilityState>
            <div className="sub capability-summary model-name" title={models[0].name}>
              {models[0].name}
            </div>
            <div className="card-footer">
              <span>{gb(models.reduce((total, model) => total + model.size_mb, 0))} GiB total</span>
              <span className="hash" title={models[0].sha256}>sha256:{models[0].sha256.slice(0, 8)}…</span>
            </div>
          </>
        )}
      </Card>
    </section>
  )
}

type CardProps = {
  title: string
  error?: string
  className?: string
  children: ReactNode
}

function Card({ title, error, className = '', children }: CardProps) {
  return (
    <article className={`card ${className}`.trim()}>
      <div className="card-title">{title}</div>
      {error ? <InlineError title="Not available" message={error} /> : children}
    </article>
  )
}

function CapabilityState({ tone, children }: { tone: 'pass' | 'fail'; children: ReactNode }) {
  return (
    <div className={`capability-state capability-state-${tone}`}>
      <span className="state-dot" aria-hidden="true" />
      <span>{children}</span>
    </div>
  )
}

function LoadingValue() {
  return <div className="value muted">Loading…</div>
}

function EmptyValue({ value, detail }: { value: string; detail: string }) {
  return (
    <div className="empty-value">
      <div className="value">{value}</div>
      <div className="sub">{detail}</div>
    </div>
  )
}

function InlineError({ title, message }: { title: string; message: string }) {
  return (
    <div className="inline-error" role="alert">
      <div className="value">{title}</div>
      <div className="error">{message}</div>
    </div>
  )
}
