import type { ReactNode } from 'react'
import { api } from '../api'
import { usePoll } from '../usePoll'

const gb = (mb: number) => (mb / 1024).toFixed(1)

export function StatusCards() {
  const status = usePoll(api.status, 2000)
  const caps = usePoll(api.capabilities, 30000)
  const metrics = usePoll(api.metrics, 2000)

  const gpu = caps.data?.gpus[0]
  const live = metrics.data?.gpus.find((m) => m.uuid === gpu?.uuid)
  const podman = caps.data?.runtimes.podman
  const cdi = caps.data?.runtimes.cdi

  return (
    <section className="cards">
      <Card title="Agent State" error={status.error}>
        <div className="value">{status.data?.state ?? '-'}</div>
        <div className="sub">{status.data?.lease_id ?? 'no active lease'}</div>
      </Card>

      <Card title="Mesh IP" error={status.error}>
        <div className="value">{status.data?.mesh_ip ?? '-'}</div>
        <div className="sub">coordinator: {status.data?.coordinator_status ?? '-'}</div>
      </Card>

      <Card title="VRAM Usage" error={caps.error ?? metrics.error}>
        {gpu && live ? (
          <>
            <div className="value">
              {gb(live.vram_used_mb)} / {gb(gpu.vram_total_mb)} GB
            </div>
            <div className="bar">
              <div style={{ width: `${(100 * live.vram_used_mb) / gpu.vram_total_mb}%` }} />
            </div>
            <div className="sub">{gpu.name}</div>
          </>
        ) : (
          <div className="value">{caps.data ? 'no GPU' : '-'}</div>
        )}
      </Card>

      <Card title="CDI Passthrough" error={caps.error}>
        <div className="value">{cdi ? (cdi.available ? cdi.devices.at(-1) : 'unavailable') : '-'}</div>
        <div className="sub">
          driver {caps.data?.driver.version || '-'} | CUDA {caps.data?.driver.cuda_version || '-'}
        </div>
      </Card>

      <Card title="Container Sandbox" error={caps.error}>
        <div className="value">
          {podman ? (podman.installed ? (podman.rootless ? 'Rootless Podman' : 'Podman (rootful)') : 'Podman missing') : '-'}
        </div>
        <div className="sub">{podman?.version ? `v${podman.version}` : ''}</div>
      </Card>
    </section>
  )
}

function Card({ title, error, children }: { title: string; error?: string; children: ReactNode }) {
  return (
    <div className="card">
      <div className="card-title">{title}</div>
      {error ? <div className="error">{error}</div> : children}
    </div>
  )
}
