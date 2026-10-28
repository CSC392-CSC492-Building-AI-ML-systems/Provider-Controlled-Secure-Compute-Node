import { useEffect, useState } from 'react'
import { api, type ProbeRow } from '../api'

export function ValidationMatrix() {
  const [rows, setRows] = useState<ProbeRow[]>([])
  const [error, setError] = useState<string>()
  const [running, setRunning] = useState<string>()
  const [exporting, setExporting] = useState(false)

  useEffect(() => {
    api.probes().then(setRows, (reason: unknown) => setError(errorMessage(reason)))
  }, [])

  const runAll = async () => {
    setRunning('all')
    try {
      setRows(await api.runAllProbes())
      setError(undefined)
    } catch (reason) {
      setError(errorMessage(reason))
    } finally {
      setRunning(undefined)
    }
  }

  const runOne = async (id: string) => {
    setRunning(id)
    try {
      const row = await api.runProbe(id)
      setRows((current) => current.map((item) => (item.id === id ? row : item)))
      setError(undefined)
    } catch (reason) {
      setError(errorMessage(reason))
    } finally {
      setRunning(undefined)
    }
  }

  const exportReport = async () => {
    setExporting(true)
    try {
      const [capabilities, probes] = await Promise.all([api.capabilities(), api.probes()])
      setRows(probes)

      const exportedAt = new Date().toISOString()
      const report = JSON.stringify({ exported_at: exportedAt, capabilities, probes }, null, 2)
      const url = URL.createObjectURL(new Blob([report], { type: 'application/json' }))
      const link = document.createElement('a')
      link.href = url
      link.download = `provider-capability-report-${exportedAt.replace(/[:.]/g, '-')}.json`
      document.body.appendChild(link)
      link.click()
      link.remove()
      URL.revokeObjectURL(url)
      setError(undefined)
    } catch (reason) {
      setError(`Could not export report: ${errorMessage(reason)}`)
    } finally {
      setExporting(false)
    }
  }

  const passed = rows.filter((row) => row.status === 'pass').length
  const busy = running !== undefined || exporting

  return (
    <section className="panel validation-matrix">
      <header>
        <div>
          <h2>
            Architecture Validation Matrix{' '}
            <span className="badge">
              {passed} / {rows.length} verified
            </span>
          </h2>
          <p className="panel-description">Run and export evidence for each security boundary</p>
        </div>
        <div className="header-actions">
          <button disabled={busy} onClick={exportReport}>
            {exporting ? 'Exporting…' : 'Export Report'}
          </button>
          <button className="primary" disabled={busy} onClick={runAll}>
            {running === 'all' ? 'Running…' : 'Run Full Audit'}
          </button>
        </div>
      </header>

      {error && <div className="panel-alert error" role="alert">{error}</div>}

      <div className="table-scroll">
        <table>
          <thead>
            <tr>
              <th>Subsystem</th>
              <th>Requirement</th>
              <th>Result</th>
              <th>Last checked</th>
              <th>Status</th>
              <th><span className="sr-only">Actions</span></th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr key={row.id}>
                <td>
                  <strong>{row.name}</strong>
                  <span className="table-meta">{row.layer} · {row.owner}</span>
                </td>
                <td className="muted requirement-cell">{row.requirement}</td>
                <td className="probe-message">{row.message}</td>
                <td className="checked-at">{formatCheckedAt(row.checked_at)}</td>
                <td>
                  <span className={`status status-${row.status}`}>{row.status.replace('_', ' ')}</span>
                </td>
                <td>
                  <button className="link" disabled={busy} onClick={() => runOne(row.id)}>
                    {running === row.id ? 'Running…' : 'Probe'}
                  </button>
                </td>
              </tr>
            ))}
            {rows.length === 0 && !error && (
              <tr>
                <td className="table-empty" colSpan={6}>Loading validation probes…</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  )
}

function formatCheckedAt(value?: string) {
  if (!value) return 'Not checked'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString([], {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

function errorMessage(reason: unknown) {
  return reason instanceof Error ? reason.message : String(reason)
}
