package collector

import "time"

// Snapshot is the complete state of the cluster pushed to browser clients every scrape.
type Snapshot struct {
	Timestamp time.Time       `json:"ts"`
	Agents    []AgentSnap     `json:"agents"`
	History   []HistoryPoint  `json:"history"`  // last 60 points (5 min at 5s interval)
	Failovers []FailoverEvent `json:"failovers"`
	Events    []EventEntry    `json:"events"`   // last 100 events since dashboard start
}

// EventEntry is a single entry in the event log (state transitions + manual actions).
type EventEntry struct {
	TS     int64  `json:"ts"`     // unix milliseconds
	AZ     string `json:"az"`
	Kind   string `json:"kind"`   // "failover","peer_down","peer_up","agent_appeared","agent_lost","route_claimed"
	Detail string `json:"detail"`
}

// AgentSnap is the scraped + derived state for a single NAT agent.
type AgentSnap struct {
	AZ               string   `json:"az"`
	InstanceID       string   `json:"instance_id"`
	InstanceType     string   `json:"instance_type"`     // e.g. "m5.large"
	TxBytesPerSec    float64  `json:"tx_bps"`
	RxBytesPerSec    float64  `json:"rx_bps"`
	ConntrackEntries float64  `json:"conntrack_entries"`
	ConntrackMax     float64  `json:"conntrack_max"`
	ConntrackRatio   float64  `json:"conntrack_ratio"`
	RouteTablesOwned []string `json:"route_tables"`
	PeerUp           bool     `json:"peer_up"`
	SpotPending      bool     `json:"spot_pending"`
	RulePresent      bool     `json:"rule_present"`
	SrcDstDisabled   bool     `json:"src_dst_disabled"`
	LastFailoverTS   float64  `json:"last_failover_ts"`  // unix seconds, 0 if never
	MaxBandwidthBps  float64  `json:"max_bw_bps"`        // peak network bandwidth in bytes/s
	CPUUsageRatio    float64  `json:"cpu_usage_ratio"`   // 0–1
	MemUsedBytes     float64  `json:"mem_used_bytes"`
	MemTotalBytes    float64  `json:"mem_total_bytes"`
}

// HistoryPoint is one bandwidth sample for the sparkline.
type HistoryPoint struct {
	TS    int64   `json:"ts"` // unix milliseconds
	TxBps float64 `json:"tx"`
	RxBps float64 `json:"rx"`
}

// FailoverEvent is a single takeover extracted from metric label changes.
type FailoverEvent struct {
	FromAZ string  `json:"from_az"`
	ToAZ   string  `json:"to_az"`
	TS     float64 `json:"ts"` // unix seconds from kube_nat_last_failover_seconds
}
