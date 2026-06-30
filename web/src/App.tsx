import { useWebSocket } from './hooks/useWebSocket'
import { Header } from './components/Header'
import { SummaryCards } from './components/SummaryCards'
import { AZCard } from './components/AZCard'
import { BandwidthChart } from './components/BandwidthChart'
import { EventLog } from './components/EventLog'

const DOCS_URL = 'https://mfeldheim.github.io/kube-nat/v0.1.x/'
const GITHUB_URL = 'https://github.com/mfeldheim/kube-nat'

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
      <Header agents={snap.agents} serverVersion={snap.server_version} />
      <SummaryCards agents={snap.agents} failovers={snap.failovers} />
      <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
        {snap.agents.map((a) => (
          <AZCard key={a.az} agent={a} />
        ))}
      </div>
      <BandwidthChart history={snap.history} />
      <EventLog events={snap.events ?? []} />
      <footer className="flex flex-col gap-2 border-t border-white/5 pt-5 text-xs text-gray-400 sm:flex-row sm:items-center sm:justify-between">
        <div>Copyright © 2026 kube-nat</div>
        <div className="flex items-center gap-4">
          <a href={DOCS_URL} target="_blank" rel="noreferrer" className="transition-colors hover:text-sky-300">
            Docs
          </a>
          <a
            href={GITHUB_URL}
            target="_blank"
            rel="noreferrer"
            aria-label="GitHub repository"
            className="text-gray-400 transition-colors hover:text-white"
          >
            <svg className="h-4 w-4" viewBox="0 0 16 16" fill="currentColor" aria-hidden>
              <path d="M8 0C3.58 0 0 3.58 0 8a8 8 0 0 0 5.47 7.59c.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.5-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82a7.7 7.7 0 0 1 4 0c1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8z" />
            </svg>
          </a>
        </div>
      </footer>
    </div>
  )
}
