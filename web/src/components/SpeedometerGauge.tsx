import { useFlash } from '../hooks/useFlash'

interface Props {
  value: number
  max: number
  color: string
  label: string
  formatValue?: (v: number) => string
  large?: boolean
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
export function SpeedometerGauge({ value, max, color, label, formatValue = fmtBps, large = false }: Props) {
  const pct = max > 0 ? Math.min(value / max, 1) : 0
  const arcLen = Math.PI * 28
  const filled = pct * arcLen
  const gap = arcLen - filled
  const gradId = `g-${label}-${color.replace('#', '')}`
  const w = large ? 108 : 78
  const h = large ? 66  : 48
  const flashing = useFlash(formatValue(value))

  return (
    <div className="flex flex-col items-center">
      <svg
        width={w}
        height={h}
        viewBox="0 0 72 44"
        aria-label={`${label} ${(pct * 100).toFixed(0)}%`}
      >
        <defs>
          <linearGradient id={gradId} x1="0" y1="0" x2="1" y2="0">
            <stop offset="0%"   stopColor={color} stopOpacity="0.35" />
            <stop offset="100%" stopColor={color} stopOpacity="1" />
          </linearGradient>
          <filter id={`${gradId}-glow`} x="-50%" y="-50%" width="200%" height="200%">
            <feGaussianBlur stdDeviation="1.4" result="blur" />
            <feMerge>
              <feMergeNode in="blur" />
              <feMergeNode in="SourceGraphic" />
            </feMerge>
          </filter>
        </defs>
        {/* track */}
        <path
          d="M8,38 A28,28 0 0,1 64,38"
          fill="none"
          stroke="rgba(255,255,255,0.08)"
          strokeWidth="5"
          strokeLinecap="round"
        />
        {/* fill */}
        <path
          d="M8,38 A28,28 0 0,1 64,38"
          fill="none"
          stroke={`url(#${gradId})`}
          strokeWidth="5"
          strokeLinecap="round"
          strokeDasharray={`${filled.toFixed(2)} ${gap.toFixed(2)}`}
          filter={`url(#${gradId}-glow)`}
          style={{ transition: 'stroke-dasharray 700ms ease' }}
        />
        {/* percentage */}
        <text
          x="36"
          y="32"
          textAnchor="middle"
          fontSize="10"
          fill="#cbd5e1"
          fontFamily="'JetBrains Mono', monospace"
          fontWeight="600"
        >
          {(pct * 100).toFixed(0)}%
        </text>
      </svg>
      <div className="label-eyebrow -mt-1">{label}</div>
      <div
        key={flashing ? 'flash' : 'still'}
        className={`text-xs text-gray-100 font-semibold num mt-0.5 ${flashing ? 'animate-value-flash' : ''}`}
      >
        {formatValue(value)}
      </div>
    </div>
  )
}
