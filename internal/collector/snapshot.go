package collector

import "time"

// Snapshot is the complete state of the cluster pushed to browser clients every scrape.
type Snapshot struct {
	Timestamp time.Time       `json:"ts"`
	Agents    []AgentSnap     `json:"agents"`
	History   []HistoryPoint  `json:"history"`  // last 60 points (5 min at 5s interval)
	Failovers []FailoverEvent `json:"failovers"`
}

// AgentSnap is the scraped + derived state for a single NAT agent.
type AgentSnap struct {
	AZ               string   `json:"az"`
	InstanceID       string   `json:"instance_id"`
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
	LastFailoverTS   float64  `json:"last_failover_ts"` // unix seconds, 0 if never
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
