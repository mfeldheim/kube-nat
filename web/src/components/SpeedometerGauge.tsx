interface Props {
  value: number
  max: number
  color: string  // e.g. "#34d399" or "#60a5fa"
  label: string  // "TX" or "RX"
}

function fmtBps(bps: number): string {
  if (bps >= 1e9) return `${(bps / 1e9).toFixed(1)} GB/s`
  if (bps >= 1e6) return `${(bps / 1e6).toFixed(1)} MB/s`
  if (bps >= 1e3) return `${(bps / 1e3).toFixed(1)} KB/s`
  return `${bps.toFixed(0)} B/s`
}

// Half-arc (semicircle) SVG speedometer gauge.
// Arc: M8,38 A28,28 0 0,1 64,38 — centre (36,38), radius 28, sweeps upward left→right.
// Arc length = π * 28 ≈ 87.96
export function SpeedometerGauge({ value, max, color, label }: Props) {
  const pct = max > 0 ? Math.min(value / max, 1) : 0
  const arcLen = Math.PI * 28
  const filled = pct * arcLen
  const gap = arcLen - filled

  return (
    <div className="flex flex-col items-center">
      <svg width="72" height="44" viewBox="0 0 72 44" aria-label={`${label} ${(pct * 100).toFixed(0)}%`}>
        {/* track */}
        <path
          d="M8,38 A28,28 0 0,1 64,38"
          fill="none"
          stroke="#1f2937"
          strokeWidth="5"
          strokeLinecap="round"
        />
        {/* fill */}
        <path
          d="M8,38 A28,28 0 0,1 64,38"
          fill="none"
          stroke={color}
          strokeWidth="5"
          strokeLinecap="round"
          strokeDasharray={`${filled.toFixed(2)} ${gap.toFixed(2)}`}
        />
        {/* percentage */}
        <text
          x="36"
          y="31"
          textAnchor="middle"
          fontSize="10"
          fill="#9ca3af"
          fontFamily="monospace"
        >
          {(pct * 100).toFixed(0)}%
        </text>
      </svg>
      <div className="text-xs text-gray-400 -mt-1">{label}</div>
      <div className="text-xs text-gray-100 font-bold">{fmtBps(value)}</div>
    </div>
  )
}
