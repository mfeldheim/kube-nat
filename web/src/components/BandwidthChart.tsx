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
    t: new Date(p.ts).toLocaleTimeString(undefined, {
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    }),
    tx: p.tx,
    rx: p.rx,
  }))

  return (
    <div className="panel p-5 animate-fade-up">
      <div className="flex items-center justify-between mb-4">
        <div>
          <div className="label-eyebrow">Bandwidth</div>
          <div className="text-sm text-gray-400 mt-0.5">Last 5 minutes</div>
        </div>
        <div className="flex items-center gap-4 text-xs">
          <Legend color="#34d399" label="TX" />
          <Legend color="#60a5fa" label="RX" />
        </div>
      </div>
      <ResponsiveContainer width="100%" height={200}>
        <AreaChart data={data} margin={{ top: 4, right: 8, left: 0, bottom: 0 }}>
          <defs>
            <linearGradient id="tx" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%"   stopColor="#34d399" stopOpacity={0.55} />
              <stop offset="60%"  stopColor="#34d399" stopOpacity={0.15} />
              <stop offset="100%" stopColor="#34d399" stopOpacity={0} />
            </linearGradient>
            <linearGradient id="rx" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%"   stopColor="#60a5fa" stopOpacity={0.55} />
              <stop offset="60%"  stopColor="#60a5fa" stopOpacity={0.15} />
              <stop offset="100%" stopColor="#60a5fa" stopOpacity={0} />
            </linearGradient>
            <filter id="line-glow" x="-20%" y="-20%" width="140%" height="140%">
              <feGaussianBlur stdDeviation="1.4" />
            </filter>
          </defs>
          <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.06)" />
          <XAxis
            dataKey="t"
            tick={{ fill: '#6b7280', fontSize: 10 }}
            interval="preserveStartEnd"
            axisLine={{ stroke: 'rgba(255,255,255,0.08)' }}
            tickLine={false}
          />
          <YAxis
            tickFormatter={fmtBytes}
            tick={{ fill: '#6b7280', fontSize: 10 }}
            width={40}
            axisLine={false}
            tickLine={false}
          />
          <Tooltip
            contentStyle={{
              background: 'rgba(10,14,26,0.92)',
              border: '1px solid rgba(255,255,255,0.08)',
              borderRadius: 10,
              fontSize: 12,
              boxShadow: '0 8px 28px -8px rgba(0,0,0,0.6)',
              backdropFilter: 'blur(8px)',
            }}
            labelStyle={{ color: '#94a3b8', marginBottom: 4 }}
            formatter={(v: number, name: string) => [`${fmtBytes(v)} B/s`, name === 'tx' ? 'TX' : 'RX']}
          />
          <Area
            type="monotone"
            dataKey="tx"
            stroke="#34d399"
            fill="url(#tx)"
            strokeWidth={2}
            dot={false}
            activeDot={{ r: 4, strokeWidth: 0, fill: '#34d399' }}
          />
          <Area
            type="monotone"
            dataKey="rx"
            stroke="#60a5fa"
            fill="url(#rx)"
            strokeWidth={2}
            dot={false}
            activeDot={{ r: 4, strokeWidth: 0, fill: '#60a5fa' }}
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  )
}

function Legend({ color, label }: { color: string; label: string }) {
  return (
    <span className="inline-flex items-center gap-1.5 text-gray-400">
      <span
        className="h-2 w-2 rounded-full"
        style={{ background: color, boxShadow: `0 0 10px ${color}` }}
      />
      {label}
    </span>
  )
}
