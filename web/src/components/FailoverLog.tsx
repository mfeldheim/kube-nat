import type { FailoverEvent } from '../types'

interface Props {
  failovers: FailoverEvent[]
}

const dateTimeFmt = new Intl.DateTimeFormat(undefined, {
  year: 'numeric',
  month: 'short',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false,
  timeZoneName: 'short',
})

const fullFmt = new Intl.DateTimeFormat(undefined, {
  dateStyle: 'full',
  timeStyle: 'long',
})

export function FailoverLog({ failovers }: Props) {
  const sorted = [...failovers].sort((a, b) => b.ts - a.ts).slice(0, 20)

  return (
    <div className="panel p-5 animate-fade-up">
      <div className="label-eyebrow mb-3">Failover events</div>
      {sorted.length === 0 ? (
        <div className="text-gray-500 text-sm py-8 text-center border border-dashed border-white/10 rounded-xl">
          No failover events.
        </div>
      ) : (
        <table className="w-full text-sm">
          <thead>
            <tr className="text-gray-500 text-[10px] uppercase tracking-[0.18em]">
              <th className="text-left font-medium py-2 px-2">Time</th>
              <th className="text-left font-medium py-2 px-2">From AZ</th>
              <th className="text-left font-medium py-2 px-2">Covered by</th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((f, i) => (
              <tr key={i} className="border-t border-white/[0.04]">
                <td
                  className="py-2 px-2 text-gray-400 num"
                  title={fullFmt.format(new Date(f.ts * 1000))}
                >
                  {dateTimeFmt.format(new Date(f.ts * 1000))}
                </td>
                <td className="py-2 px-2 text-gray-200">{f.from_az}</td>
                <td className="py-2 px-2 text-emerald-300">{f.to_az || '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}
