package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Registry holds all kube-nat metric definitions.
type Registry struct {
	BytesTX   *prometheus.CounterVec
	BytesRX   *prometheus.CounterVec
	PacketsTX *prometheus.CounterVec
	PacketsRX *prometheus.CounterVec

	ConntrackEntries    prometheus.Gauge
	ConntrackMax        prometheus.Gauge
	ConntrackUsageRatio prometheus.Gauge

	RulePresent         *prometheus.GaugeVec
	SrcDstCheckDisabled prometheus.Gauge
	RouteTableOwned     *prometheus.GaugeVec

	PeerStatus    *prometheus.GaugeVec
	FailoverTotal *prometheus.CounterVec
	LastFailover  *prometheus.GaugeVec

	SpotInterruptionPending prometheus.Gauge
	MaxBandwidthBps         prometheus.Gauge

	// Smoothed bandwidth rates updated every second by the agent.
	TxBytesPerSec *prometheus.GaugeVec
	RxBytesPerSec *prometheus.GaugeVec

	reg *prometheus.Registry
}

func NewRegistry() *Registry {
	r := prometheus.NewRegistry()
	m := &Registry{reg: r}

	ifaceLabels := []string{"az", "instance_id", "iface"}

	m.BytesTX = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kube_nat_bytes_tx_total", Help: "Total bytes transmitted",
	}, ifaceLabels)
	m.BytesRX = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kube_nat_bytes_rx_total", Help: "Total bytes received",
	}, ifaceLabels)
	m.PacketsTX = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kube_nat_packets_tx_total", Help: "Total packets transmitted",
	}, ifaceLabels)
	m.PacketsRX = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kube_nat_packets_rx_total", Help: "Total packets received",
	}, ifaceLabels)
	m.ConntrackEntries = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kube_nat_conntrack_entries", Help: "Current number of conntrack entries",
	})
	m.ConntrackMax = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kube_nat_conntrack_max", Help: "Maximum conntrack entries (nf_conntrack_max)",
	})
	m.ConntrackUsageRatio = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kube_nat_conntrack_usage_ratio", Help: "Conntrack usage ratio (entries/max). Alert at >0.7",
	})
	m.RulePresent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kube_nat_rule_present", Help: "1 if the iptables rule is present, 0 if missing",
	}, []string{"rule"})
	m.SrcDstCheckDisabled = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kube_nat_src_dst_check_disabled", Help: "1 if source/dest check is disabled on the ENI",
	})
	m.RouteTableOwned = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kube_nat_route_table_owned", Help: "1 if this node owns the route table",
	}, []string{"rtb_id"})
	m.PeerStatus = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kube_nat_peer_status", Help: "1=peer up, 0=peer down",
	}, []string{"az", "instance_id"})
	m.FailoverTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kube_nat_failover_total", Help: "Number of route table takeovers performed",
	}, []string{"from_az", "to_az"})
	m.LastFailover = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kube_nat_last_failover_seconds", Help: "Unix timestamp of last failover for an AZ",
	}, []string{"az"})
	m.SpotInterruptionPending = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kube_nat_spot_interruption_pending", Help: "1 if a spot interruption notice has been received",
	})
	m.MaxBandwidthBps = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kube_nat_max_bandwidth_bps",
		Help: "Peak network bandwidth of this instance in bytes/s (from EC2 DescribeInstanceTypes)",
	})
	m.TxBytesPerSec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kube_nat_tx_bps",
		Help: "EMA-smoothed TX throughput in bytes/s, updated every second",
	}, []string{"az", "instance_id"})
	m.RxBytesPerSec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kube_nat_rx_bps",
		Help: "EMA-smoothed RX throughput in bytes/s, updated every second",
	}, []string{"az", "instance_id"})

	r.MustRegister(
		m.BytesTX, m.BytesRX, m.PacketsTX, m.PacketsRX,
		m.ConntrackEntries, m.ConntrackMax, m.ConntrackUsageRatio,
		m.RulePresent, m.SrcDstCheckDisabled, m.RouteTableOwned,
		m.PeerStatus, m.FailoverTotal, m.LastFailover,
		m.SpotInterruptionPending, m.MaxBandwidthBps,
		m.TxBytesPerSec, m.RxBytesPerSec,
	)
	return m
}

func (m *Registry) Prometheus() *prometheus.Registry {
	return m.reg
}
