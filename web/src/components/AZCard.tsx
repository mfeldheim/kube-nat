import { useState, useEffect, type ReactNode } from 'react'
import type { AgentSnap } from '../types'
import { SpeedometerGauge } from './SpeedometerGauge'
import { useFlash } from '../hooks/useFlash'

interface Props {
  agent: AgentSnap
}

type BtnState = 'idle' | 'loading' | 'ok' | 'error'

export function AZCard({ agent: a }: Props) {
  const healthy = a.rule_present && a.src_dst_disabled
  const connRatio = a.conntrack_max > 0 ? a.conntrack_entries / a.conntrack_max : 0
  const connColor = connRatio > 0.7 ? '#ef4444' : connRatio > 0.5 ? '#f59e0b' : '#a78bfa'
  const [claimState, setClaimState]     = useState<BtnState>('idle')
  const [releaseState, setReleaseState] = useState<BtnState>('idle')
  const [optimisticReleased, setOptimisticReleased] = useState(false)

  useEffect(() => {
    if ((a.route_tables?.length ?? 0) === 0) setOptimisticReleased(false)
  }, [a.route_tables])

  const hasRoutes = !optimisticReleased && (a.route_tables?.length ?? 0) > 0

  const instanceType = a.instance_type  || '—'
  const memTotal     = a.mem_total_bytes
  const memUsed      = a.mem_used_bytes
  const cpuRatio     = a.cpu_usage_ratio

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
    <div className="panel panel-hover px-3 pb-3 pt-2 space-y-2 animate-fade-up overflow-hidden">
      {/* accent glow */}
      <div
        aria-hidden
        className={`pointer-events-none absolute -top-16 -right-16 h-40 w-40 rounded-full blur-3xl ${
          healthy ? 'bg-emerald-500/10' : 'bg-rose-500/15'
        }`}
      />

      {/* ── Row 1: AZ name + status dot + SPOT badge ── */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2.5 min-w-0">
          <span className="relative flex h-2.5 w-2.5 shrink-0">
            <span className={`absolute inline-flex h-full w-full rounded-full opacity-70 animate-pulse-dot ${healthy ? 'bg-emerald-400' : 'bg-rose-400'}`} />
            <span className={`relative inline-flex h-2.5 w-2.5 rounded-full ${healthy ? 'bg-emerald-400 shadow-glow-green' : 'bg-rose-400 shadow-glow-red'}`} />
          </span>
          <span className="font-bold text-2xl text-gray-100 tracking-tight">{a.az}</span>
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

      {/* ── Row 2: Instance facts ── */}
      <div className="flex flex-wrap gap-x-3 gap-y-1.5 text-[11px]">
        <Fact icon={<IconServer />} label={instanceType} title="Instance type" mono />
        <span className="text-gray-700 select-none">·</span>
        <Fact icon={<IconID />} label={a.instance_id || '—'} title="Instance ID" mono />
        {hasRoutes && (
          <>
            <span className="text-gray-700 select-none">·</span>
            <Fact icon={<IconRoute />} label={a.route_tables.join(', ')} title="Owned route tables" mono />
          </>
        )}
      </div>

      {/* ── Row 3: Speed gauges ── */}
      <div className="flex gap-3 justify-center py-0.5">
        <SpeedometerGauge value={a.tx_bps}          max={a.max_bw_bps ?? 0} color="#34d399" label="TX"   />
        <SpeedometerGauge value={a.rx_bps}          max={a.max_bw_bps ?? 0} color="#60a5fa" label="RX"   />
        <SpeedometerGauge value={a.conntrack_entries} max={a.conntrack_max}  color={connColor} label="conn" formatValue={fmtCount} />
      </div>

      {/* ── Row 4: Resource stats ── */}
      <div className="space-y-1.5 pt-0.5">
        <StatBar
          icon={<IconMemory />}
          label="mem"
          value={`${fmtGB(memUsed)} / ${fmtGB(memTotal)}`}
          ratio={memTotal > 0 ? memUsed / memTotal : 0}
          from="from-violet-500" to="to-fuchsia-400"
          warn={(memTotal > 0 ? memUsed / memTotal : 0) > 0.8}
        />
        <StatBar
          icon={<IconCPU />}
          label="cpu"
          value={`${(cpuRatio * 100).toFixed(1)}%`}
          ratio={cpuRatio}
          from="from-sky-500" to="to-indigo-400"
          warn={cpuRatio > 0.8}
        />
        {a.max_bw_bps > 0 && (
          <div className="flex items-center gap-2 text-[11px] text-gray-500">
            <IconNetwork />
            <span className="text-gray-500 w-7">net</span>
            <span className="text-gray-300 num">{(a.max_bw_bps / 1e9).toFixed(0)} Gbps peak</span>
          </div>
        )}
      </div>

      {/* ── Row 5: Status chips + action button ── */}
      <div className="flex items-center justify-between gap-2 flex-wrap pt-0.5 border-t border-white/[0.05]">
        <div className="flex gap-1.5">
          <Flag ok={a.rule_present}    label="iptables" />
          <Flag ok={a.src_dst_disabled} label="src/dst" />
          <Flag ok={a.peer_up}          label="peer" />
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
              {releaseState === 'loading' ? <><Spinner /> Releasing…</> :
               releaseState === 'ok'      ? 'Released ✓' :
               releaseState === 'error'   ? 'Failed ✗' : 'Fallback to NAT'}
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
              {claimState === 'loading' ? <><Spinner /> Claiming…</> :
               claimState === 'ok'      ? 'Claimed ✓' :
               claimState === 'error'   ? 'Failed ✗' : 'Claim routes'}
            </button>
          )}
        </div>
      </div>
    </div>
  )
}

// ── Helpers ────────────────────────────────────────────────

function fmtGB(b: number): string {
  if (b >= 1e9) return `${(b / 1e9).toFixed(1)} GB`
  if (b >= 1e6) return `${(b / 1e6).toFixed(0)} MB`
  return '—'
}

function Fact({ icon, label, title, mono }: { icon: ReactNode; label: string; title?: string; mono?: boolean }) {
  return (
    <span className="flex items-center gap-1" title={title}>
      {icon}
      <span className={`text-gray-400 ${mono ? 'font-mono' : ''}`}>{label || '—'}</span>
    </span>
  )
}

function StatBar({ icon, label, value, ratio, from, to, warn }: {
  icon: ReactNode; label: string; value: string
  ratio: number; from: string; to: string; warn: boolean
}) {
  const flashing = useFlash(value)
  return (
    <div className="flex items-center gap-2">
      {icon}
      <span className="text-[11px] text-gray-500 w-7 shrink-0">{label}</span>
      <div className="flex-1 h-1.5 bg-white/5 rounded-full overflow-hidden">
        <div
          className={`h-full rounded-full bg-gradient-to-r transition-all duration-700 ${
            warn ? 'from-rose-500 to-rose-400' : `${from} ${to}`
          }`}
          style={{ width: `${Math.min(ratio * 100, 100).toFixed(1)}%` }}
        />
      </div>
      <span
        key={flashing ? 'flash' : 'still'}
        className={`text-[11px] text-gray-300 num w-24 text-right shrink-0 ${flashing ? 'animate-value-flash' : ''}`}
      >
        {value}
      </span>
    </div>
  )
}

function Flag({ ok, label }: { ok: boolean; label: string }) {
  return (
    <span className={`chip ${ok ? 'chip-ok' : 'chip-bad'}`}>
      <span className={`h-1.5 w-1.5 rounded-full ${ok ? 'bg-emerald-400' : 'bg-rose-400'}`} />
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

// ── Icons ──────────────────────────────────────────────────

function IconServer() {
  return (
    <svg className="h-3 w-3 shrink-0 text-gray-500" viewBox="0 0 16 16" fill="currentColor" aria-hidden>
      <path d="M2 3a1 1 0 0 1 1-1h10a1 1 0 0 1 1 1v2a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1V3zm0 6a1 1 0 0 1 1-1h10a1 1 0 0 1 1 1v2a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1V9zm11-5.5a.5.5 0 1 1-1 0 .5.5 0 0 1 1 0zm0 6a.5.5 0 1 1-1 0 .5.5 0 0 1 1 0z"/>
    </svg>
  )
}

function IconID() {
  return (
    <svg className="h-3 w-3 shrink-0 text-gray-500" viewBox="0 0 16 16" fill="currentColor" aria-hidden>
      <path d="M5 8a1 1 0 1 1-2 0 1 1 0 0 1 2 0zm4-3H5a.5.5 0 0 0 0 1h4a.5.5 0 0 0 0-1zM3 5.5a.5.5 0 0 1 .5-.5h9a.5.5 0 0 1 0 1h-9a.5.5 0 0 1-.5-.5zM3.5 7h4a.5.5 0 0 1 0 1h-4a.5.5 0 0 1 0-1z"/>
      <path d="M14 3a1 1 0 0 1 1 1v8a1 1 0 0 1-1 1H2a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h12zM2 2a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V4a2 2 0 0 0-2-2H2z"/>
    </svg>
  )
}

function IconRoute() {
  return (
    <svg className="h-3 w-3 shrink-0 text-gray-500" viewBox="0 0 16 16" fill="currentColor" aria-hidden>
      <path fillRule="evenodd" d="M8.636 3.5a.5.5 0 0 0-.5-.5H1.5A1.5 1.5 0 0 0 0 4.5v10A1.5 1.5 0 0 0 1.5 16h10a1.5 1.5 0 0 0 1.5-1.5V7.864a.5.5 0 0 0-1 0V14.5a.5.5 0 0 1-.5.5h-10a.5.5 0 0 1-.5-.5v-10a.5.5 0 0 1 .5-.5h6.636a.5.5 0 0 0 .5-.5z"/>
      <path fillRule="evenodd" d="M16 .5a.5.5 0 0 0-.5-.5h-5a.5.5 0 0 0 0 1h3.793L6.146 9.146a.5.5 0 1 0 .708.708L15 1.707V5.5a.5.5 0 0 0 1 0v-5z"/>
    </svg>
  )
}

function IconMemory() {
  return (
    <svg className="h-3 w-3 shrink-0 text-gray-500" viewBox="0 0 16 16" fill="currentColor" aria-hidden>
      <path d="M1 3a1 1 0 0 0-1 1v8a1 1 0 0 0 1 1h4.586A2 2 0 0 0 7 12.586l7-7A2 2 0 0 0 14 4H1zm4 6H2V5h3v4zm2 0V5h2v4H7zm4 0H9V5h2v4zm2-4v4h-1V5h1zM2 10h2v1H2v-1z"/>
    </svg>
  )
}

function IconCPU() {
  return (
    <svg className="h-3 w-3 shrink-0 text-gray-500" viewBox="0 0 16 16" fill="currentColor" aria-hidden>
      <path d="M5 0a.5.5 0 0 1 .5.5V2h1V.5a.5.5 0 0 1 1 0V2h1V.5a.5.5 0 0 1 1 0V2h1V.5a.5.5 0 0 1 1 0V2A2.5 2.5 0 0 1 14 4.5h1.5a.5.5 0 0 1 0 1H14v1h1.5a.5.5 0 0 1 0 1H14v1h1.5a.5.5 0 0 1 0 1H14v1h1.5a.5.5 0 0 1 0 1H14a2.5 2.5 0 0 1-2.5 2.5v1.5a.5.5 0 0 1-1 0V14h-1v1.5a.5.5 0 0 1-1 0V14h-1v1.5a.5.5 0 0 1-1 0V14h-1v1.5a.5.5 0 0 1-1 0V14A2.5 2.5 0 0 1 2 11.5H.5a.5.5 0 0 1 0-1H2v-1H.5a.5.5 0 0 1 0-1H2v-1H.5a.5.5 0 0 1 0-1H2v-1H.5a.5.5 0 0 1 0-1H2A2.5 2.5 0 0 1 4.5 2V.5A.5.5 0 0 1 5 0zm-.5 3A1.5 1.5 0 0 0 3 4.5v7A1.5 1.5 0 0 0 4.5 13h7a1.5 1.5 0 0 0 1.5-1.5v-7A1.5 1.5 0 0 0 11.5 3h-7zM5 6.5A1.5 1.5 0 0 1 6.5 5h3A1.5 1.5 0 0 1 11 6.5v3A1.5 1.5 0 0 1 9.5 11h-3A1.5 1.5 0 0 1 5 9.5v-3z"/>
    </svg>
  )
}

function IconNetwork() {
  return (
    <svg className="h-3 w-3 shrink-0 text-gray-500" viewBox="0 0 16 16" fill="currentColor" aria-hidden>
      <path fillRule="evenodd" d="M11.5 15a.5.5 0 0 0 .5-.5V2.707l1.146 1.147a.5.5 0 0 0 .708-.708l-2-2a.5.5 0 0 0-.708 0l-2 2a.5.5 0 1 0 .708.708L11 2.707V14.5a.5.5 0 0 0 .5.5zm-7-14a.5.5 0 0 1 .5.5v11.793l1.146-1.147a.5.5 0 0 1 .708.708l-2 2a.5.5 0 0 1-.708 0l-2-2a.5.5 0 0 1 .708-.708L4 13.293V1.5a.5.5 0 0 1 .5-.5z"/>
    </svg>
  )
}
