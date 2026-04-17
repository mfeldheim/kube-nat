import type { AgentSnap } from '../types'

interface Props {
  agents: AgentSnap[]
}

export function Header({ agents }: Props) {
  const healthy = agents.filter((a) => a.rule_present && a.src_dst_disabled).length
  const status = healthy === agents.length && agents.length > 0 ? 'Healthy' : 'Degraded'
  const statusColor = status === 'Healthy' ? 'text-green-400' : 'text-red-400'
  const azCount = new Set(agents.map((a) => a.az)).size

  return (
    <header className="flex items-center justify-between border-b border-gray-800 pb-4">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">kube-nat</h1>
        <p className="text-gray-400 text-sm">Real-time NAT dashboard</p>
      </div>
      <div className="text-right">
        <div className={`text-lg font-semibold ${statusColor}`}>{status}</div>
        <div className="text-gray-400 text-sm">
          {agents.length} node{agents.length !== 1 ? 's' : ''} · {azCount} AZ{azCount !== 1 ? 's' : ''}
        </div>
      </div>
    </header>
  )
}
