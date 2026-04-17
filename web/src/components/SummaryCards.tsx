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

  return (
    <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
      <Card label="TX" value={fmtBps(totalTx)} />
      <Card label="RX" value={fmtBps(totalRx)} />
      <Card label="Connections" value={totalConn.toLocaleString()}>
        <div className="mt-2 h-1.5 bg-gray-800 rounded">
          <div
            className={`h-full rounded ${connRatio > 0.7 ? 'bg-red-500' : 'bg-green-500'}`}
            style={{ width: `${Math.min(connRatio * 100, 100).toFixed(1)}%` }}
          />
        </div>
        <div className="text-xs text-gray-500 mt-1">{(connRatio * 100).toFixed(1)}% of limit</div>
      </Card>
      <Card label="Failovers (24h)" value={String(fo24h)} />
    </div>
  )
}

function Card({ label, value, children }: { label: string; value: string; children?: ReactNode }) {
  return (
    <div className="bg-gray-900 border border-gray-800 rounded-lg p-4">
      <div className="text-gray-400 text-xs uppercase tracking-widest mb-1">{label}</div>
      <div className="text-2xl font-bold">{value}</div>
      {children}
    </div>
  )
}
