import { useCallback, useEffect, useState } from 'react'
import { api, type ProbeRow } from '../api'

export function ValidationMatrix() {
  const [rows, setRows] = useState<ProbeRow[]>([])
  const [error, setError] = useState<string>()
  const [running, setRunning] = useState<string>()

  const run = useCallback(async (id: string, call: () => Promise<ProbeRow[]>) => {
    setRunning(id)
    try {
      setRows(await call())
      setError(undefined)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setRunning(undefined)
    }
  }, [])

  useEffect(() => {
    api.probes().then(setRows, (e: unknown) => setError(e instanceof Error ? e.message : String(e)))
  }, [])

  const runOne = (id: string) =>
    run(id, async () => {
      const row = await api.runProbe(id)
      return rows.map((r) => (r.id === id ? row : r))
    })

  const passed = rows.filter((r) => r.status === 'pass').length

  return (
    <section className="panel">
      <header>
        <h2>
          Architecture Validation Matrix{' '}
          <span className="badge">
            {passed} / {rows.length} verified
          </span>
        </h2>
        <button disabled={running !== undefined} onClick={() => run('all', api.runAllProbes)}>
          {running === 'all' ? 'Running...' : 'Run Full Audit'}
        </button>
      </header>
      {error && <div className="error">{error}</div>}
      <table>
        <thead>
          <tr>
            <th>Subsystem</th>
            <th>Requirement</th>
            <th>Layer</th>
            <th>Owner</th>
            <th>Status</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.id} title={r.message}>
              <td>{r.name}</td>
              <td className="muted">{r.requirement}</td>
              <td className="muted">{r.layer}</td>
              <td className="muted">{r.owner}</td>
              <td>
                <span className={`status status-${r.status}`}>{r.status.replace('_', ' ')}</span>
              </td>
              <td>
                <button className="link" disabled={running !== undefined} onClick={() => runOne(r.id)}>
                  {running === r.id ? '...' : 'Probe'}
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  )
}
