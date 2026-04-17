import { useEffect, useRef, useState } from 'react'
import type { Snapshot } from '../types'

export function useWebSocket(): Snapshot | null {
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null)
  const retryRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    let ws: WebSocket | null = null
    let dead = false

    function connect() {
      const proto = location.protocol === 'https:' ? 'wss' : 'ws'
      ws = new WebSocket(`${proto}://${location.host}/ws`)

      ws.onmessage = (ev) => {
        try {
          setSnapshot(JSON.parse(ev.data) as Snapshot)
        } catch {
          // ignore parse errors
        }
      }

      ws.onclose = () => {
        if (!dead) {
          retryRef.current = setTimeout(connect, 2000)
        }
      }

      ws.onerror = () => ws?.close()
    }

    connect()

    return () => {
      dead = true
      if (retryRef.current) clearTimeout(retryRef.current)
      ws?.close()
    }
  }, [])

  return snapshot
}
