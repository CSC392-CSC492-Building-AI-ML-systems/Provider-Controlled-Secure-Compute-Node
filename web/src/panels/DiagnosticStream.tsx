import { useEffect, useState } from 'react'
import { subscribeEvents, type NodeEvent } from '../api'

const maxEvents = 200

export function DiagnosticStream() {
  const [events, setEvents] = useState<NodeEvent[]>([])

  useEffect(() => subscribeEvents((e) => setEvents((prev) => [...prev, e].slice(-maxEvents))), [])

  return (
    <section className="panel">
      <header>
        <h2>Diagnostic Stream</h2>
        <button className="link" onClick={() => setEvents([])}>
          Clear
        </button>
      </header>
      <div className="log">
        {events.length === 0 && <div className="muted">waiting for events...</div>}
        {events.map((e, i) => (
          <div key={i} className={`log-${e.level}`}>
            {new Date(e.time).toLocaleTimeString()} [{e.source}] {e.message}
          </div>
        ))}
      </div>
    </section>
  )
}
