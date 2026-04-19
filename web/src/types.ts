export interface AgentSnap {
  az: string
  instance_id: string
  tx_bps: number
  rx_bps: number
  conntrack_entries: number
  conntrack_max: number
  conntrack_ratio: number
  route_tables: string[]
  peer_up: boolean
  spot_pending: boolean
  rule_present: boolean
  src_dst_disabled: boolean
  last_failover_ts: number  // unix seconds, 0 if never
}

export interface HistoryPoint {
  ts: number    // unix milliseconds
  tx: number    // bytes per second
  rx: number
}

export interface FailoverEvent {
  from_az: string
  to_az: string
  ts: number    // unix seconds
}

export interface EventEntry {
  ts: number    // unix milliseconds
  az: string
  kind: 'failover' | 'peer_down' | 'peer_up' | 'agent_appeared' | 'agent_lost' | 'route_claimed'
  detail: string
}

export interface Snapshot {
  ts: string          // ISO timestamp
  agents: AgentSnap[]
  history: HistoryPoint[]
  failovers: FailoverEvent[]
  events: EventEntry[]
}
