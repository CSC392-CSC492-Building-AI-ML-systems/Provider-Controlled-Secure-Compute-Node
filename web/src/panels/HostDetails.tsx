import { api } from '../api'
import { usePoll } from '../usePoll'

const gib = (mb: number) => `${(mb / 1024).toFixed(1)} GiB`

export function HostDetails() {
  const caps = usePoll(api.capabilities, 30000)

  return (
    <section className="panel host-details">
      <header>
        <div>
          <h2>Host &amp; GPU Details</h2>
          <p className="panel-description">Hardware and driver capabilities reported by this node</p>
        </div>
        {caps.data && <span className="collected-at">Updated {formatTime(caps.data.collected_at)}</span>}
      </header>

      {caps.error ? (
        <div className="panel-empty panel-error" role="alert">
          <strong>Capability details are unavailable</strong>
          <span>{caps.error}</span>
        </div>
      ) : !caps.data ? (
        <div className="panel-empty">Loading host details…</div>
      ) : (
        <>
          {caps.data.probe_errors.length > 0 && (
            <div className="probe-error-summary">
              {caps.data.probe_errors.map((error) => (
                <span key={`${error.probe}-${error.error}`}>
                  <strong>{error.probe}:</strong> {error.error}
                </span>
              ))}
            </div>
          )}

          <div className="detail-columns">
            <div className="detail-group">
              <h3>Host</h3>
              <dl className="detail-list">
                <Detail label="CPU" value={caps.data.host.cpu_model || 'Unknown'} />
                <Detail label="CPU cores" value={valueOrUnknown(caps.data.host.cpu_cores)} />
                <Detail label="Memory" value={caps.data.host.ram_total_mb ? gib(caps.data.host.ram_total_mb) : 'Unknown'} />
                <Detail label="Operating system" value={caps.data.host.os || 'Unknown'} />
                <Detail label="Architecture" value={caps.data.host.arch || 'Unknown'} />
                <Detail label="Kernel" value={caps.data.host.kernel || 'Unknown'} />
              </dl>
            </div>

            <div className="detail-group">
              <h3>GPU &amp; Driver</h3>
              <dl className="detail-list">
                <Detail label="Driver" value={caps.data.driver.version || 'Unknown'} />
                <Detail label="CUDA" value={caps.data.driver.cuda_version || 'Unknown'} />
                <Detail label="GPU count" value={caps.data.gpus.length} />
              </dl>

              {caps.data.gpus.length === 0 ? (
                <div className="no-gpu-detail">
                  <strong>No GPU detected</strong>
                  <span>The host is online, but no NVIDIA GPU capability was reported.</span>
                </div>
              ) : (
                <div className="gpu-list">
                  {caps.data.gpus.map((gpu) => (
                    <article className="gpu-detail" key={gpu.uuid}>
                      <div className="gpu-detail-heading">
                        <strong>{gpu.name}</strong>
                        <span>GPU {gpu.index}</span>
                      </div>
                      <div className="gpu-facts">
                        <span>{gib(gpu.vram_total_mb)} VRAM</span>
                        <span>Compute {gpu.compute_capability.major}.{gpu.compute_capability.minor}</span>
                        <span>{gpu.mig_enabled ? 'MIG enabled' : 'MIG disabled'}</span>
                      </div>
                      <code title={gpu.uuid}>{gpu.uuid}</code>
                    </article>
                  ))}
                </div>
              )}
            </div>
          </div>
        </>
      )}
    </section>
  )
}

function Detail({ label, value }: { label: string; value: string | number }) {
  return (
    <>
      <dt>{label}</dt>
      <dd>{value}</dd>
    </>
  )
}

function valueOrUnknown(value: number) {
  return value > 0 ? value : 'Unknown'
}

function formatTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}
