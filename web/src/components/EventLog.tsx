import type { EventEntry } from '../types'

interface Props {
  events: EventEntry[]
}

const kindLabel: Record<string, string> = {
  failover:       'FAILOVER',
  peer_down:      'PEER DOWN',
  peer_up:        'PEER UP',
  agent_appeared: 'AGENT UP',
  agent_lost:     'AGENT LOST',
  route_claimed:  'ROUTE CLAIM',
}

const kindColor: Record<string, string> = {
  failover:       'bg-red-900 text-red-300',
  peer_down:      'bg-orange-900 text-orange-300',
  peer_up:        'bg-green-900 text-green-300',
  agent_appeared: 'bg-blue-900 text-blue-300',
  agent_lost:     'bg-red-900 text-red-300',
  route_claimed:  'bg-purple-900 text-purple-300',
}

export function EventLog({ events }: Props) {
  const sorted = [...events].sort((a, b) => b.ts - a.ts).slice(0, 50)

  return (
    <div className="bg-gray-900 border border-gray-800 rounded-lg p-4">
      <div className="text-gray-400 text-xs uppercase tracking-widest mb-3">
        Event log
      </div>
      {sorted.length === 0 ? (
        <div className="text-gray-600 text-sm">No events yet.</div>
      ) : (
        <table className="w-full text-sm">
          <thead>
            <tr className="text-gray-500 text-xs border-b border-gray-800">
              <th className="text-left py-1 pr-4 whitespace-nowrap">Time</th>
              <th className="text-left py-1 pr-4 whitespace-nowrap">AZ</th>
              <th className="text-left py-1 pr-4 whitespace-nowrap">Type</th>
              <th className="text-left py-1">Detail</th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((e, i) => (
              <tr key={i} className="border-b border-gray-800/50">
                <td className="py-1 pr-4 text-gray-400 whitespace-nowrap">
                  {new Date(e.ts).toLocaleTimeString()}
                </td>
                <td className="py-1 pr-4 whitespace-nowrap">{e.az}</td>
                <td className="py-1 pr-4 whitespace-nowrap">
                  <span className={`px-1.5 py-0.5 rounded text-xs ${kindColor[e.kind] ?? 'bg-gray-800 text-gray-300'}`}>
                    {kindLabel[e.kind] ?? e.kind}
                  </span>
                </td>
                <td className="py-1 text-gray-400 text-xs">{e.detail}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}
