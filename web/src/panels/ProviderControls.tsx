import { useState } from 'react'
import { api } from '../api'
import { usePoll } from '../usePoll'

export function ProviderControls() {
  const status = usePoll(api.status, 2000)
  const [result, setResult] = useState<string>()

  const act = async (label: string, call: () => Promise<unknown>) => {
    if (!window.confirm(`${label} this node?`)) return
    try {
      await call()
      setResult(`${label} accepted`)
    } catch (e) {
      setResult(`${label} failed: ${e instanceof Error ? e.message : String(e)}`)
    }
    status.refresh()
  }

  return (
    <section className="panel">
      <header>
        <h2>Provider Controls</h2>
      </header>
      <div className="controls">
        <button onClick={status.refresh}>Status</button>
        <button className="warn" onClick={() => act('Drain', () => api.drain())}>
          Drain
        </button>
        <button className="danger" onClick={() => act('Reclaim', api.reclaim)}>
          Reclaim
        </button>
      </div>
      {status.error ? (
        <div className="error">{status.error}</div>
      ) : (
        <dl>
          <dt>State</dt>
          <dd>{status.data?.state ?? '-'}</dd>
          <dt>Active lease</dt>
          <dd>{status.data?.lease_id ?? 'none'}</dd>
          <dt>Last heartbeat</dt>
          <dd>{status.data?.last_heartbeat_at ? new Date(status.data.last_heartbeat_at).toLocaleTimeString() : '-'}</dd>
        </dl>
      )}
      {result && <div className="muted">{result}</div>}
    </section>
  )
}
