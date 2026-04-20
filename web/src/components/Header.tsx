import type { AgentSnap } from '../types'

interface Props {
  agents: AgentSnap[]
}

export function Header({ agents }: Props) {
  const healthy = agents.filter((a) => a.rule_present && a.src_dst_disabled).length
  const isHealthy = healthy === agents.length && agents.length > 0
  const status = isHealthy ? 'Healthy' : 'Degraded'
  const azCount = new Set(agents.map((a) => a.az)).size

  return (
    <header className="animate-fade-in">
      <div className="flex items-center justify-between gap-4">
        <div className="flex items-baseline gap-2">
          <h1 className="text-3xl sm:text-4xl font-extrabold tracking-tight bg-gradient-to-r from-sky-300 via-indigo-300 to-emerald-300 bg-clip-text text-transparent drop-shadow-[0_2px_18px_rgba(96,165,250,0.35)]">
            kube<span className="text-white">NAT</span>
          </h1>
          <span className="hidden sm:inline text-xs text-gray-500 font-mono uppercase tracking-[0.2em]">
            dashboard
          </span>
        </div>

        <div className="flex items-center gap-3">
          <div
            className={`chip ${isHealthy ? 'chip-ok' : 'chip-bad'} px-3 py-1 text-xs`}
            aria-label={`System ${status}`}
          >
            <span className="relative flex h-2 w-2">
              <span
                className={`absolute inline-flex h-full w-full rounded-full opacity-60 animate-pulse-dot ${
                  isHealthy ? 'bg-emerald-400' : 'bg-rose-400'
                }`}
              />
              <span
                className={`relative inline-flex h-2 w-2 rounded-full ${
                  isHealthy ? 'bg-emerald-400' : 'bg-rose-400'
                }`}
              />
            </span>
            <span className="font-semibold">{status}</span>
          </div>
          <div className="hidden md:flex items-center gap-3 text-xs text-gray-400">
            <span className="chip chip-muted num">
              {agents.length} node{agents.length !== 1 ? 's' : ''}
            </span>
            <span className="chip chip-muted num">
              {azCount} AZ{azCount !== 1 ? 's' : ''}
            </span>
          </div>
        </div>
      </div>
      <div
        className="divider-glow animate-gradient-shift mt-4"
        style={{ backgroundSize: '200% 100%' }}
      />
    </header>
  )
}
