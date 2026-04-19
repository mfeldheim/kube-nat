import { useState } from 'react'
import type { AgentSnap } from '../types'
import { SpeedometerGauge } from './SpeedometerGauge'

interface Props {
  agent: AgentSnap
}

type BtnState = 'idle' | 'loading' | 'ok' | 'error'

export function AZCard({ agent: a }: Props) {
  const statusDot = a.rule_present && a.src_dst_disabled ? 'bg-green-400' : 'bg-red-400'
  const connPct = a.conntrack_max > 0 ? (a.conntrack_entries / a.conntrack_max) * 100 : 0
  const connBarColor = connPct > 70 ? 'bg-red-500' : connPct > 50 ? 'bg-yellow-400' : 'bg-green-500'
  const [claimState, setClaimState] = useState<BtnState>('idle')
  const [releaseState, setReleaseState] = useState<BtnState>('idle')

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
      setReleaseState(resp.ok ? 'ok' : 'error')
    } catch {
      setReleaseState('error')
    }
    setTimeout(() => setReleaseState('idle'), 3000)
  }

  return (
    <div className="bg-gray-900 border border-gray-800 rounded-lg p-4 space-y-3">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <span className={`w-2 h-2 rounded-full ${statusDot}`} />
          <span className="font-semibold">{a.az}</span>
        </div>
        {a.spot_pending && (
          <span className="text-xs bg-orange-800 text-orange-200 px-2 py-0.5 rounded">
            SPOT ⚠
          </span>
        )}
      </div>

      <div className="text-xs text-gray-400">{a.instance_id || '—'}</div>

      <div className="flex gap-3 justify-center">
        <SpeedometerGauge value={a.tx_bps} max={a.max_bw_bps ?? 0} color="#34d399" label="TX" />
        <SpeedometerGauge value={a.rx_bps} max={a.max_bw_bps ?? 0} color="#60a5fa" label="RX" />
      </div>

      <div>
        <div className="flex justify-between text-xs text-gray-400 mb-1">
          <span>Conntrack</span>
          <span>{a.conntrack_entries.toLocaleString()} / {a.conntrack_max.toLocaleString()}</span>
        </div>
        <div className="h-1.5 bg-gray-800 rounded">
          <div
            className={`h-full rounded ${connBarColor} transition-all duration-500`}
            style={{ width: `${Math.min(connPct, 100).toFixed(1)}%` }}
          />
        </div>
        <div className="text-xs text-gray-500 mt-0.5">{connPct.toFixed(1)}%</div>
      </div>

      {a.route_tables?.length > 0 && (
        <div className="text-xs text-gray-400">Routes: {a.route_tables.join(', ')}</div>
      )}

      <div className="flex items-center justify-between">
        <div className="flex gap-2 text-xs">
          <Flag ok={a.rule_present} label="iptables" />
          <Flag ok={a.src_dst_disabled} label="src/dst" />
          <Flag ok={a.peer_up} label="peer" />
        </div>

        <div className="flex gap-2">
          {a.route_tables?.length > 0 && (
            <button
              onClick={handleRelease}
              disabled={releaseState === 'loading'}
              className={`text-xs px-2 py-1 rounded whitespace-nowrap transition-colors ${
                releaseState === 'loading' ? 'bg-gray-700 text-gray-400 cursor-wait' :
                releaseState === 'ok'      ? 'bg-green-800 text-green-200' :
                releaseState === 'error'   ? 'bg-red-800 text-red-200' :
                'bg-yellow-900 text-yellow-300 hover:bg-yellow-800'
              }`}
            >
              {releaseState === 'loading' ? 'Releasing…' :
               releaseState === 'ok'      ? 'Released ✓' :
               releaseState === 'error'   ? 'Failed ✗' :
               'Fallback to NAT'}
            </button>
          )}

          {!(a.route_tables?.length > 0) && (
            <button
              onClick={handleClaim}
              disabled={claimState === 'loading'}
              className={`text-xs px-2 py-1 rounded whitespace-nowrap transition-colors ${
                claimState === 'loading' ? 'bg-gray-700 text-gray-400 cursor-wait' :
                claimState === 'ok'      ? 'bg-green-800 text-green-200' :
                claimState === 'error'   ? 'bg-red-800 text-red-200' :
                'bg-gray-700 text-gray-300 hover:bg-gray-600'
              }`}
            >
              {claimState === 'loading' ? 'Claiming…' :
               claimState === 'ok'      ? 'Claimed ✓' :
               claimState === 'error'   ? 'Failed ✗' :
               'Claim routes'}
            </button>
          )}
        </div>
      </div>
    </div>
  )
}

function Flag({ ok, label }: { ok: boolean; label: string }) {
  return (
    <span className={`px-1.5 py-0.5 rounded ${ok ? 'bg-green-900 text-green-300' : 'bg-red-900 text-red-300'}`}>
      {label}
    </span>
  )
}
