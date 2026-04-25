import type { EventEntry } from '../types'

interface Props {
  events: EventEntry[]
}

const timeFmt = new Intl.DateTimeFormat(undefined, {
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false,
  timeZoneName: 'short',
})

const fullFmt = new Intl.DateTimeFormat(undefined, {
  dateStyle: 'medium',
  timeStyle: 'long',
})

const kindLabel: Record<string, string> = {
  failover:       'FAILOVER',
  peer_down:      'PEER DOWN',
  peer_up:        'PEER UP',
  agent_appeared: 'AGENT UP',
  agent_lost:     'AGENT LOST',
  route_update:   'ROUTE UPDATE',
  route_regained: 'REGAINED',
}

const kindChip: Record<string, string> = {
  failover:       'chip-bad',
  peer_down:      'chip-warn',
  peer_up:        'chip-ok',
  agent_appeared: 'chip-info',
  agent_lost:     'chip-bad',
  route_update:   'chip chip-muted !border-violet-400/30 !bg-violet-400/10 !text-violet-300',
  route_regained: 'chip chip-muted !border-green-400/30 !bg-green-400/10 !text-green-300',
}

export function EventLog({ events }: Props) {
  const sorted = [...events].sort((a, b) => b.ts - a.ts).slice(0, 50)

  return (
    <div className="panel p-5 animate-fade-up">
      <div className="flex items-center justify-between mb-3">
        <div>
          <div className="label-eyebrow">Event log</div>
          <div className="text-sm text-gray-400 mt-0.5">
            {sorted.length > 0 ? `${sorted.length} recent event${sorted.length !== 1 ? 's' : ''}` : 'Live'}
          </div>
        </div>
      </div>
      {sorted.length === 0 ? (
        <div className="text-gray-500 text-sm py-8 text-center border border-dashed border-white/10 rounded-xl">
          No events yet.
        </div>
      ) : (
        <div className="overflow-x-auto -mx-1">
          <table className="w-full text-sm">
            <thead>
              <tr className="text-gray-500 text-[10px] uppercase tracking-[0.18em]">
                <th className="text-left font-medium py-2 px-2 whitespace-nowrap">Time</th>
                <th className="text-left font-medium py-2 px-2 whitespace-nowrap">AZ</th>
                <th className="text-left font-medium py-2 px-2 whitespace-nowrap">Type</th>
                <th className="text-left font-medium py-2 px-2">Detail</th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((e, i) => (
                <tr
                  key={i}
                  className="border-t border-white/[0.04] hover:bg-white/[0.02] transition-colors"
                >
                  <td
                    className="py-2 px-2 text-gray-400 whitespace-nowrap num"
                    title={fullFmt.format(new Date(e.ts))}
                  >
                    {timeFmt.format(new Date(e.ts))}
                  </td>
                  <td className="py-2 px-2 whitespace-nowrap font-medium text-gray-200">{e.az}</td>
                  <td className="py-2 px-2 whitespace-nowrap">
                    <span className={`chip ${kindChip[e.kind] ?? 'chip-muted'}`}>
                      {kindLabel[e.kind] ?? e.kind}
                    </span>
                  </td>
                  <td className="py-2 px-2 text-gray-400 text-xs">{e.detail}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
