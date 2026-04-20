import { useState, useEffect } from 'react'
import type { AgentSnap } from '../types'
import { SpeedometerGauge } from './SpeedometerGauge'

interface Props {
  agent: AgentSnap
}

type BtnState = 'idle' | 'loading' | 'ok' | 'error'

export function AZCard({ agent: a }: Props) {
  const healthy = a.rule_present && a.src_dst_disabled
  const connRatio = a.conntrack_max > 0 ? a.conntrack_entries / a.conntrack_max : 0
  const connColor = connRatio > 0.7 ? '#ef4444' : connRatio > 0.5 ? '#f59e0b' : '#a78bfa'
  const [claimState, setClaimState] = useState<BtnState>('idle')
  const [releaseState, setReleaseState] = useState<BtnState>('idle')
  const [optimisticReleased, setOptimisticReleased] = useState(false)

  useEffect(() => {
    if ((a.route_tables?.length ?? 0) === 0) setOptimisticReleased(false)
  }, [a.route_tables])

  const hasRoutes = !optimisticReleased && (a.route_tables?.length ?? 0) > 0

  function fmtCount(v: number): string {
    if (v >= 1e6) return `${(v / 1e6).toFixed(1)}M`
    if (v >= 1e3) return `${(v / 1e3).toFixed(0)}k`
    return String(Math.round(v))
  }

  async function handleClaim() {
    setClaimState('loading')
    try {
      const resp = await fetch(`/agents/${encodeURIComponent(a.az)}/claim`, { method: 'POST' })
      setClaimState(resp.ok ? 'ok' : 'error')
    } catch {
      setClaimState('error')
    }
    setTimeout(() => setClaimState('idle'), 3000)
  }

  async function handleRelease() {
    setReleaseState('loading')
    try {
      const resp = await fetch(`/agents/${encodeURIComponent(a.az)}/release`, { method: 'POST' })
      if (resp.ok) {
        setOptimisticReleased(true)
        setReleaseState('ok')
      } else {
        setReleaseState('error')
      }
    } catch {
      setReleaseState('error')
    }
    setTimeout(() => setReleaseState('idle'), 3000)
  }

  return (
    <div className="panel panel-hover p-4 space-y-4 animate-fade-up overflow-hidden">
      {/* accent glow */}
      <div
        aria-hidden
        className={`pointer-events-none absolute -top-16 -right-16 h-40 w-40 rounded-full blur-3xl ${
          healthy
            ? 'bg-emerald-500/10'
            : 'bg-rose-500/15'
        }`}
      />

      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2.5 min-w-0">
          <span className="relative flex h-2.5 w-2.5 shrink-0">
            <span
              className={`absolute inline-flex h-full w-full rounded-full opacity-70 animate-pulse-dot ${
                healthy ? 'bg-emerald-400' : 'bg-rose-400'
              }`}
            />
            <span
              className={`relative inline-flex h-2.5 w-2.5 rounded-full ${
                healthy ? 'bg-emerald-400' : 'bg-rose-400'
              } ${healthy ? 'shadow-glow-green' : 'shadow-glow-red'}`}
            />
          </span>
          <span className="font-semibold text-gray-100 tracking-tight truncate">{a.az}</span>
        </div>
        {a.spot_pending && (
          <span className="chip chip-warn">
            <svg className="h-3 w-3" viewBox="0 0 20 20" fill="currentColor" aria-hidden>
              <path fillRule="evenodd" d="M8.485 2.495c.673-1.167 2.357-1.167 3.03 0l6.28 10.875c.673 1.167-.17 2.625-1.516 2.625H3.72c-1.347 0-2.189-1.458-1.515-2.625L8.485 2.495zM10 6a.75.75 0 01.75.75v3.5a.75.75 0 01-1.5 0v-3.5A.75.75 0 0110 6zm0 9a1 1 0 100-2 1 1 0 000 2z" clipRule="evenodd" />
            </svg>
            SPOT
          </span>
        )}
      </div>

      <div className="text-[11px] text-gray-500 font-mono truncate">{a.instance_id || '—'}</div>

      <div className="flex gap-3 justify-center py-1">
        <SpeedometerGauge value={a.tx_bps} max={a.max_bw_bps ?? 0} color="#34d399" label="TX" />
        <SpeedometerGauge value={a.rx_bps} max={a.max_bw_bps ?? 0} color="#60a5fa" label="RX" />
        <SpeedometerGauge
          value={a.conntrack_entries}
          max={a.conntrack_max}
          color={connColor}
          label="conn"
          formatValue={fmtCount}
        />
      </div>

      {hasRoutes && (
        <div className="text-[11px] text-gray-400">
          <span className="label-eyebrow mr-2">Routes</span>
          <span className="font-mono text-gray-300">{a.route_tables.join(', ')}</span>
        </div>
      )}

      <div className="flex items-center justify-between gap-2 flex-wrap">
        <div className="flex gap-1.5 text-xs">
          <Flag ok={a.rule_present} label="iptables" />
          <Flag ok={a.src_dst_disabled} label="src/dst" />
          <Flag ok={a.peer_up} label="peer" />
        </div>

        <div className="flex gap-2">
          {hasRoutes && (
            <button
              onClick={handleRelease}
              disabled={releaseState === 'loading'}
              className={`btn whitespace-nowrap ${
                releaseState === 'loading' ? 'border-white/10 bg-white/5 text-gray-400 cursor-wait' :
                releaseState === 'ok'      ? 'border-emerald-400/30 bg-emerald-400/10 text-emerald-300' :
                releaseState === 'error'   ? 'border-rose-400/30 bg-rose-400/10 text-rose-300' :
                'btn-warn'
              }`}
            >
              {releaseState === 'loading' ? (
                <>
                  <Spinner /> Releasing…
                </>
              ) : releaseState === 'ok' ? (
                'Released ✓'
              ) : releaseState === 'error' ? (
                'Failed ✗'
              ) : (
                'Fallback to NAT'
              )}
            </button>
          )}

          {!hasRoutes && (
            <button
              onClick={handleClaim}
              disabled={claimState === 'loading'}
              className={`btn whitespace-nowrap ${
                claimState === 'loading' ? 'border-white/10 bg-white/5 text-gray-400 cursor-wait' :
                claimState === 'ok'      ? 'border-emerald-400/30 bg-emerald-400/10 text-emerald-300' :
                claimState === 'error'   ? 'border-rose-400/30 bg-rose-400/10 text-rose-300' :
                'btn-ghost'
              }`}
            >
              {claimState === 'loading' ? (
                <>
                  <Spinner /> Claiming…
                </>
              ) : claimState === 'ok' ? (
                'Claimed ✓'
              ) : claimState === 'error' ? (
                'Failed ✗'
              ) : (
                'Claim routes'
              )}
            </button>
          )}
        </div>
      </div>
    </div>
  )
}

function Flag({ ok, label }: { ok: boolean; label: string }) {
  return (
    <span className={`chip ${ok ? 'chip-ok' : 'chip-bad'}`}>
      <span
        className={`h-1.5 w-1.5 rounded-full ${ok ? 'bg-emerald-400 shadow-glow-green' : 'bg-rose-400 shadow-glow-red'}`}
      />
      {label}
    </span>
  )
}

function Spinner() {
  return (
    <svg className="h-3 w-3 animate-spin" viewBox="0 0 24 24" aria-hidden>
      <circle cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="3" fill="none" opacity="0.25" />
      <path d="M22 12a10 10 0 0 0-10-10" stroke="currentColor" strokeWidth="3" fill="none" strokeLinecap="round" />
    </svg>
  )
}
