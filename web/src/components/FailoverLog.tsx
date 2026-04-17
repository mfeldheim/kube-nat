import type { FailoverEvent } from '../types'

interface Props {
  failovers: FailoverEvent[]
}

export function FailoverLog({ failovers }: Props) {
  const sorted = [...failovers].sort((a, b) => b.ts - a.ts).slice(0, 20)

  return (
    <div className="bg-gray-900 border border-gray-800 rounded-lg p-4">
      <div className="text-gray-400 text-xs uppercase tracking-widest mb-3">
        Failover events
      </div>
      {sorted.length === 0 ? (
        <div className="text-gray-600 text-sm">No failover events.</div>
      ) : (
        <table className="w-full text-sm">
          <thead>
            <tr className="text-gray-500 text-xs border-b border-gray-800">
              <th className="text-left py-1">Time</th>
              <th className="text-left py-1">From AZ</th>
              <th className="text-left py-1">Covered by</th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((f, i) => (
              <tr key={i} className="border-b border-gray-800/50">
                <td className="py-1 text-gray-400">
                  {new Date(f.ts * 1000).toLocaleString()}
                </td>
                <td className="py-1">{f.from_az}</td>
                <td className="py-1 text-green-400">{f.to_az || '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}
