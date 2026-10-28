import { DiagnosticStream } from './panels/DiagnosticStream'
import { HostDetails } from './panels/HostDetails'
import { ProviderControls } from './panels/ProviderControls'
import { StatusCards } from './panels/StatusCards'
import { ValidationMatrix } from './panels/ValidationMatrix'

export default function App() {
  return (
    <main>
      <h1>Provider-Controlled Secure Compute Node</h1>
      <StatusCards />
      <HostDetails />
      <div className="grid">
        <ValidationMatrix />
        <div className="side">
          <ProviderControls />
          <DiagnosticStream />
        </div>
      </div>
    </main>
  )
}
