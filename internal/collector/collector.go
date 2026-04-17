package collector

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Config configures the Collector.
type Config struct {
	K8sClient      kubernetes.Interface
	Namespace      string
	MetricsPort    int
	ScrapeInterval time.Duration
}

// prevCounters holds the last seen counter values for rate calculation.
type prevCounters struct {
	bytesTX float64
	bytesRX float64
	ts      time.Time
}

// Collector discovers agent pods and scrapes their metrics.
type Collector struct {
	cfg     Config
	client  *http.Client
	mu      sync.Mutex
	prev    map[string]prevCounters // keyed by pod IP
	history []HistoryPoint          // ring buffer, max 60 entries
	seen    map[string]float64      // last seen kube_nat_last_failover_seconds per AZ
}

// New creates a Collector.
func New(cfg Config) *Collector {
	return &Collector{
		cfg:    cfg,
		client: &http.Client{Timeout: 3 * time.Second},
		prev:   make(map[string]prevCounters),
		seen:   make(map[string]float64),
	}
}

// Collect discovers all agent pods, scrapes metrics, and returns a Snapshot.
func (c *Collector) Collect(ctx context.Context) (*Snapshot, error) {
	pods, err := c.cfg.K8sClient.CoreV1().Pods(c.cfg.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=kube-nat,component=agent",
	})
	if err != nil {
		return nil, fmt.Errorf("list agent pods: %w", err)
	}

	var (
		agents    = make([]AgentSnap, 0)
		totalTx   float64
		totalRx   float64
		failovers = make([]FailoverEvent, 0)
	)

	for _, pod := range pods.Items {
		if pod.Status.PodIP == "" {
			continue
		}
		url := fmt.Sprintf("http://%s:%d/metrics", pod.Status.PodIP, c.cfg.MetricsPort)
		families, err := c.scrape(ctx, url)
		if err != nil {
			continue // skip unreachable agents
		}

		snap := c.buildSnap(pod.Status.PodIP, families)
		if snap == nil {
			continue
		}

		// Detect failover events from changes in kube_nat_last_failover_seconds.
		c.mu.Lock()
		if prev, ok := c.seen[snap.AZ]; ok && snap.LastFailoverTS > 0 && snap.LastFailoverTS != prev {
			failovers = append(failovers, FailoverEvent{
				FromAZ: snap.AZ,
				ToAZ:   snap.AZ,
				TS:     snap.LastFailoverTS,
			})
		}
		if snap.LastFailoverTS > 0 {
			c.seen[snap.AZ] = snap.LastFailoverTS
		}
		c.mu.Unlock()

		agents = append(agents, *snap)
		totalTx += snap.TxBytesPerSec
		totalRx += snap.RxBytesPerSec
	}

	// Maintain history ring buffer (last 60 points = 5 min at 5s interval).
	c.mu.Lock()
	c.history = append(c.history, HistoryPoint{
		TS:    time.Now().UnixMilli(),
		TxBps: totalTx,
		RxBps: totalRx,
	})
	if len(c.history) > 60 {
		c.history = c.history[len(c.history)-60:]
	}
	historyCopy := make([]HistoryPoint, len(c.history))
	copy(historyCopy, c.history)
	c.mu.Unlock()

	return &Snapshot{
		Timestamp: time.Now(),
		Agents:    agents,
		History:   historyCopy,
		Failovers: failovers,
	}, nil
}

// scrape fetches and parses Prometheus text metrics from url.
func (c *Collector) scrape(ctx context.Context, url string) (map[string]*dto.MetricFamily, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(string(body)))
	// TextToMetricFamilies returns partial results + error for EOF — use what we got.
	if families == nil {
		return nil, err
	}
	return families, nil
}

// buildSnap extracts an AgentSnap from parsed metric families.
// Returns nil if the AZ label is missing (not a kube-nat agent metric set).
func (c *Collector) buildSnap(podIP string, families map[string]*dto.MetricFamily) *AgentSnap {
	snap := &AgentSnap{}

	snap.ConntrackEntries = gaugeVal(families, "kube_nat_conntrack_entries")
	snap.ConntrackMax = gaugeVal(families, "kube_nat_conntrack_max")
	snap.ConntrackRatio = gaugeVal(families, "kube_nat_conntrack_usage_ratio")
	snap.RulePresent = gaugeVal(families, "kube_nat_rule_present") >= 1
	snap.SrcDstDisabled = gaugeVal(families, "kube_nat_src_dst_check_disabled") >= 1
	snap.SpotPending = gaugeVal(families, "kube_nat_spot_interruption_pending") >= 1

	// Collect owned route tables (all rtb_id labels with value >= 1).
	if mf, ok := families["kube_nat_route_table_owned"]; ok {
		for _, m := range mf.Metric {
			if m.Gauge != nil && m.Gauge.GetValue() >= 1 {
				for _, lp := range m.Label {
					if lp.GetName() == "rtb_id" {
						snap.RouteTablesOwned = append(snap.RouteTablesOwned, lp.GetValue())
					}
				}
			}
		}
	}

	// Read AZ + instance_id from bytes_tx labels; accumulate counter value.
	var currentTx, currentRx float64
	if mf, ok := families["kube_nat_bytes_tx_total"]; ok && len(mf.Metric) > 0 {
		m := mf.Metric[0]
		for _, lp := range m.Label {
			switch lp.GetName() {
			case "az":
				snap.AZ = lp.GetValue()
			case "instance_id":
				snap.InstanceID = lp.GetValue()
			}
		}
		if m.Counter != nil {
			currentTx = m.Counter.GetValue()
		}
	}
	if mf, ok := families["kube_nat_bytes_rx_total"]; ok && len(mf.Metric) > 0 {
		if mf.Metric[0].Counter != nil {
			currentRx = mf.Metric[0].Counter.GetValue()
		}
	}

	// Compute per-second rates using counter delta / elapsed seconds.
	c.mu.Lock()
	p, hasPrev := c.prev[podIP]
	now := time.Now()
	if hasPrev && now.Sub(p.ts) > 0 {
		elapsed := now.Sub(p.ts).Seconds()
		snap.TxBytesPerSec = (currentTx - p.bytesTX) / elapsed
		snap.RxBytesPerSec = (currentRx - p.bytesRX) / elapsed
	}
	c.prev[podIP] = prevCounters{bytesTX: currentTx, bytesRX: currentRx, ts: now}
	c.mu.Unlock()

	// Last failover timestamp.
	if mf, ok := families["kube_nat_last_failover_seconds"]; ok {
		for _, m := range mf.Metric {
			for _, lp := range m.Label {
				if lp.GetName() == "az" && lp.GetValue() == snap.AZ && m.Gauge != nil {
					snap.LastFailoverTS = m.Gauge.GetValue()
				}
			}
		}
	}

	// Any peer with status=1 means at least one peer is healthy.
	if mf, ok := families["kube_nat_peer_status"]; ok {
		for _, m := range mf.Metric {
			if m.Gauge != nil && m.Gauge.GetValue() >= 1 {
				snap.PeerUp = true
				break
			}
		}
	}

	if snap.AZ == "" {
		return nil
	}
	return snap
}

// gaugeVal returns the first gauge value for a named metric family, or 0.
func gaugeVal(families map[string]*dto.MetricFamily, name string) float64 {
	mf, ok := families[name]
	if !ok || len(mf.Metric) == 0 {
		return 0
	}
	if mf.Metric[0].Gauge != nil {
		return mf.Metric[0].Gauge.GetValue()
	}
	return 0
}
