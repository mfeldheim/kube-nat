import type { ReactNode } from 'react'
import type { AgentSnap, FailoverEvent } from '../types'

function fmtBps(bps: number): string {
  if (bps >= 1e9) return `${(bps / 1e9).toFixed(1)} GB/s`
  if (bps >= 1e6) return `${(bps / 1e6).toFixed(1)} MB/s`
  if (bps >= 1e3) return `${(bps / 1e3).toFixed(1)} KB/s`
  return `${bps.toFixed(0)} B/s`
}

interface Props {
  agents: AgentSnap[]
  failovers: FailoverEvent[]
}

export function SummaryCards({ agents, failovers }: Props) {
  const totalTx = agents.reduce((s, a) => s + a.tx_bps, 0)
  const totalRx = agents.reduce((s, a) => s + a.rx_bps, 0)
  const totalConn = agents.reduce((s, a) => s + a.conntrack_entries, 0)
  const maxConn = agents.reduce((s, a) => s + a.conntrack_max, 0)
  const connRatio = maxConn > 0 ? totalConn / maxConn : 0
  const fo24h = failovers.filter((f) => f.ts > Date.now() / 1000 - 86400).length
  const totalMaxBw = agents.reduce((s, a) => s + (a.max_bw_bps ?? 0), 0)

  return (
    <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
      <Card label="TX" value={fmtBps(totalTx)} accent="emerald" delay={0}>
        {totalMaxBw > 0 && <BwBar ratio={totalTx / totalMaxBw} from="from-emerald-400" to="to-emerald-500" />}
      </Card>
      <Card label="RX" value={fmtBps(totalRx)} accent="sky" delay={60}>
        {totalMaxBw > 0 && <BwBar ratio={totalRx / totalMaxBw} from="from-sky-400" to="to-sky-500" />}
      </Card>
      <Card label="Connections" value={totalConn.toLocaleString()} accent="violet" delay={120}>
        <div className="mt-3 h-1.5 bg-white/5 rounded-full overflow-hidden">
          <div
            className={`h-full rounded-full bg-gradient-to-r ${
              connRatio > 0.7
                ? 'from-rose-500 to-rose-400'
                : connRatio > 0.5
                ? 'from-amber-500 to-amber-400'
                : 'from-violet-500 to-fuchsia-400'
            } transition-all duration-700`}
            style={{ width: `${Math.min(connRatio * 100, 100).toFixed(1)}%` }}
          />
        </div>
        <div className="text-[11px] text-gray-500 mt-1.5 num">{(connRatio * 100).toFixed(1)}% of limit</div>
      </Card>
      <Card label="Failovers (24h)" value={String(fo24h)} accent={fo24h > 0 ? 'amber' : 'slate'} delay={180} />
    </div>
  )
}

const accentMap = {
  emerald: 'from-emerald-400/70 to-emerald-600/0',
  sky:     'from-sky-400/70 to-sky-600/0',
  violet:  'from-violet-400/70 to-fuchsia-600/0',
  amber:   'from-amber-400/70 to-amber-600/0',
  slate:   'from-slate-400/40 to-slate-600/0',
} as const

function Card({
  label,
  value,
  children,
  accent,
  delay,
}: {
  label: string
  value: string
  children?: ReactNode
  accent: keyof typeof accentMap
  delay: number
}) {
  return (
    <div
      className="panel panel-hover p-4 overflow-hidden animate-fade-up"
      style={{ animationDelay: `${delay}ms` }}
    >
      <div
        aria-hidden
        className={`pointer-events-none absolute -top-10 -right-10 h-28 w-28 rounded-full blur-3xl bg-gradient-to-br ${accentMap[accent]}`}
      />
      <div className="label-eyebrow mb-1.5">{label}</div>
      <div className="text-3xl font-bold tracking-tight num">{value}</div>
      {children}
    </div>
  )
}

function BwBar({ ratio, from, to }: { ratio: number; from: string; to: string }) {
  const pct = Math.min(ratio * 100, 100)
  return (
    <div className="mt-3">
      <div className="h-1.5 bg-white/5 rounded-full overflow-hidden">
        <div
          className={`h-full rounded-full bg-gradient-to-r ${from} ${to} transition-all duration-700`}
          style={{ width: `${pct.toFixed(1)}%` }}
        />
      </div>
      <div className="text-[11px] text-gray-500 mt-1.5 num">{pct.toFixed(1)}% of capacity</div>
    </div>
  )
}
