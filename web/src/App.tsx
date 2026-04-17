import { useWebSocket } from './hooks/useWebSocket'
import { Header } from './components/Header'
import { SummaryCards } from './components/SummaryCards'
import { AZCard } from './components/AZCard'
import { BandwidthChart } from './components/BandwidthChart'
import { FailoverLog } from './components/FailoverLog'

export default function App() {
  const snap = useWebSocket()

  if (!snap) {
    return (
      <div className="flex items-center justify-center h-screen text-gray-400">
        Connecting to kube-nat dashboard…
      </div>
    )
  }

  return (
    <div className="min-h-screen p-4 space-y-6 max-w-7xl mx-auto">
      <Header agents={snap.agents} />
      <SummaryCards agents={snap.agents} failovers={snap.failovers} />
      <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
        {snap.agents.map((a) => (
          <AZCard key={a.az} agent={a} />
        ))}
      </div>
      <BandwidthChart history={snap.history} />
      <FailoverLog failovers={snap.failovers} />
    </div>
  )
}
