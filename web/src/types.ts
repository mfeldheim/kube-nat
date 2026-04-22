export interface AgentSnap {
  az: string
  instance_id: string
  instance_type: string     // e.g. "m5.large"
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
  max_bw_bps: number        // peak network bandwidth in bytes/s
  cpu_usage_ratio: number   // 0–1
  mem_used_bytes: number
  mem_total_bytes: number
}

export interface HistoryPoint {
  ts: number    // unix milliseconds
  tx: number    // bytes per second
  rx: number
  conntrack: number  // total conntrack entries across all agents
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
