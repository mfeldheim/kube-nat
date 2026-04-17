import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  CartesianGrid,
} from 'recharts'
import type { HistoryPoint } from '../types'

interface Props {
  history: HistoryPoint[]
}

function fmtBytes(bps: number): string {
  if (bps >= 1e6) return `${(bps / 1e6).toFixed(1)}M`
  if (bps >= 1e3) return `${(bps / 1e3).toFixed(0)}K`
  return String(bps.toFixed(0))
}

export function BandwidthChart({ history }: Props) {
  const data = history.map((p) => ({
    t: new Date(p.ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' }),
    tx: p.tx,
    rx: p.rx,
  }))

  return (
    <div className="bg-gray-900 border border-gray-800 rounded-lg p-4">
      <div className="text-gray-400 text-xs uppercase tracking-widest mb-3">
        Bandwidth — last 5 min
      </div>
      <ResponsiveContainer width="100%" height={160}>
        <AreaChart data={data} margin={{ top: 4, right: 8, left: 0, bottom: 0 }}>
          <defs>
            <linearGradient id="tx" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor="#34d399" stopOpacity={0.3} />
              <stop offset="95%" stopColor="#34d399" stopOpacity={0} />
            </linearGradient>
            <linearGradient id="rx" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor="#60a5fa" stopOpacity={0.3} />
              <stop offset="95%" stopColor="#60a5fa" stopOpacity={0} />
            </linearGradient>
          </defs>
          <CartesianGrid strokeDasharray="3 3" stroke="#1f2937" />
          <XAxis dataKey="t" tick={{ fill: '#6b7280', fontSize: 10 }} interval="preserveStartEnd" />
          <YAxis tickFormatter={fmtBytes} tick={{ fill: '#6b7280', fontSize: 10 }} width={40} />
          <Tooltip
            contentStyle={{ background: '#111827', border: '1px solid #374151', fontSize: 12 }}
            formatter={(v: number, name: string) => [`${fmtBytes(v)} B/s`, name === 'tx' ? 'TX' : 'RX']}
          />
          <Area type="monotone" dataKey="tx" stroke="#34d399" fill="url(#tx)" strokeWidth={1.5} dot={false} />
          <Area type="monotone" dataKey="rx" stroke="#60a5fa" fill="url(#rx)" strokeWidth={1.5} dot={false} />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  )
}
