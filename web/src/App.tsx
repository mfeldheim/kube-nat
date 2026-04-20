import { useWebSocket } from './hooks/useWebSocket'
import { Header } from './components/Header'
import { SummaryCards } from './components/SummaryCards'
import { AZCard } from './components/AZCard'
import { BandwidthChart } from './components/BandwidthChart'
import { EventLog } from './components/EventLog'

export default function App() {
  const snap = useWebSocket()

  if (!snap) {
    return (
      <div className="flex items-center justify-center min-h-screen">
        <div className="flex flex-col items-center gap-4 animate-fade-in">
          <div className="text-3xl font-extrabold tracking-tight bg-gradient-to-r from-sky-300 via-indigo-300 to-emerald-300 bg-clip-text text-transparent">
            kube<span className="text-white">NAT</span>
          </div>
          <div className="flex items-center gap-2 text-gray-400 text-sm">
            <svg className="h-4 w-4 animate-spin text-sky-400" viewBox="0 0 24 24" aria-hidden>
              <circle cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="3" fill="none" opacity="0.25" />
              <path d="M22 12a10 10 0 0 0-10-10" stroke="currentColor" strokeWidth="3" fill="none" strokeLinecap="round" />
            </svg>
            Connecting to dashboard…
          </div>
        </div>
      </div>
    )
  }

  return (
    <div className="min-h-screen px-4 sm:px-6 lg:px-8 py-6 space-y-6 max-w-7xl mx-auto">
      <Header agents={snap.agents} />
      <SummaryCards agents={snap.agents} failovers={snap.failovers} />
      <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
        {snap.agents.map((a) => (
          <AZCard key={a.az} agent={a} />
        ))}
      </div>
      <BandwidthChart history={snap.history} />
      <EventLog events={snap.events ?? []} />
    </div>
  )
}
