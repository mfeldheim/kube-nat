# kube-nat Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the `kube-nat dashboard` subcommand — a Go collector that scrapes agent metrics every 5s and serves a real-time React SPA over WebSocket — plus all Kubernetes manifests and a Helm chart.

**Architecture:** The collector discovers agent pods via the Kubernetes API, scrapes each pod's `/metrics` endpoint, computes throughput rates, and broadcasts a JSON `Snapshot` to all WebSocket clients. The React SPA connects on load, renders per-AZ cards with live conntrack bars, a 5-minute bandwidth sparkline, and a failover event log. The compiled React app is embedded into the Go binary via `//go:embed web/dist`. Raw K8s manifests live in `deploy/manifests/`; the Helm chart in `deploy/helm/` wraps them with `values.yaml` templating.

**Tech Stack:** Go 1.22, `coder/websocket` (WebSocket server), `prometheus/common/expfmt` (Prometheus text parser — already an indirect dep), React 18 + TypeScript + Vite, Tailwind CSS, recharts (sparkline), Helm 3.

---

## File Map

```
kube-nat/
├── cmd/kube-nat/main.go                          ← add dashboardCmd()
├── internal/
│   ├── config/config.go                          ← add ScrapeInterval, DashboardPort
│   ├── collector/
│   │   ├── snapshot.go                           ← AgentSnap, Snapshot, HistoryPoint, FailoverEvent types
│   │   ├── collector.go                          ← k8s discovery + Prometheus scraping + rate calc
│   │   └── collector_test.go
│   └── dashboard/
│       ├── hub.go                                ← WebSocket client registry + broadcast
│       ├── server.go                             ← HTTP server: / (SPA), /ws, /healthz
│       └── server_test.go
├── web/
│   ├── package.json
│   ├── tsconfig.json
│   ├── vite.config.ts
│   ├── index.html
│   └── src/
│       ├── main.tsx
│       ├── App.tsx
│       ├── types.ts                              ← mirrors Go Snapshot JSON
│       ├── hooks/useWebSocket.ts
│       └── components/
│           ├── Header.tsx
│           ├── SummaryCards.tsx
│           ├── AZCard.tsx
│           ├── BandwidthChart.tsx
│           └── FailoverLog.tsx
├── deploy/
│   ├── manifests/
│   │   ├── namespace.yaml
│   │   ├── serviceaccount.yaml
│   │   ├── rbac.yaml
│   │   ├── priorityclass.yaml
│   │   ├── daemonset.yaml
│   │   ├── deployment-dashboard.yaml
│   │   ├── service.yaml
│   │   ├── pdb.yaml
│   │   └── networkpolicy.yaml
│   └── helm/
│       ├── Chart.yaml
│       ├── values.yaml
│       └── templates/
│           ├── _helpers.tpl
│           ├── namespace.yaml
│           ├── serviceaccount.yaml
│           ├── rbac.yaml
│           ├── priorityclass.yaml
│           ├── daemonset.yaml
│           ├── deployment.yaml
│           ├── service.yaml
│           ├── pdb.yaml
│           └── networkpolicy.yaml
├── Dockerfile                                    ← add Node build stage
└── Makefile                                      ← add build-web, deploy targets
```

---

### Task 1: Add dashboard config fields

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Read current config**

Run: `cat internal/config/config.go`

- [ ] **Step 2: Add the two new fields and their env var loading to `Config` and `Load()`**

Add to the `Config` struct:
```go
ScrapeInterval  time.Duration
DashboardPort   int
```

Add to `Load()` after the existing env reads:
```go
cfg.ScrapeInterval = envDuration("KUBE_NAT_SCRAPE_INTERVAL", 5*time.Second)
cfg.DashboardPort  = envInt("KUBE_NAT_DASHBOARD_PORT", 8080)
```

- [ ] **Step 3: Add a test for the new defaults**

In `internal/config/config_test.go`, add:
```go
func TestDashboardDefaults(t *testing.T) {
    cfg, err := config.Load()
    if err != nil {
        t.Fatal(err)
    }
    if cfg.ScrapeInterval != 5*time.Second {
        t.Errorf("want 5s got %v", cfg.ScrapeInterval)
    }
    if cfg.DashboardPort != 8080 {
        t.Errorf("want 8080 got %d", cfg.DashboardPort)
    }
}
```

- [ ] **Step 4: Run the test**

Run: `go test ./internal/config/... -v -run TestDashboardDefaults`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: add ScrapeInterval and DashboardPort config fields"
```

---

### Task 2: Snapshot types

**Files:**
- Create: `internal/collector/snapshot.go`

These types define the JSON contract between the Go collector and the React SPA. Get them right first.

- [ ] **Step 1: Create snapshot.go**

```go
package collector

import "time"

// Snapshot is the complete state of the cluster pushed to browser clients every scrape.
type Snapshot struct {
	Timestamp time.Time      `json:"ts"`
	Agents    []AgentSnap    `json:"agents"`
	History   []HistoryPoint `json:"history"`  // last 60 points (5 min at 5s interval)
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
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/collector/...`
Expected: no output (success)

- [ ] **Step 3: Commit**

```bash
git add internal/collector/snapshot.go
git commit -m "feat: add collector Snapshot types"
```

---

### Task 3: Collector — k8s discovery + Prometheus scraping

**Files:**
- Create: `internal/collector/collector.go`
- Create: `internal/collector/collector_test.go`

The collector discovers agent pods via the Kubernetes API, scrapes each pod's `/metrics` endpoint every `ScrapeInterval`, computes per-second rates by diffing Prometheus counter values, and returns a fresh `Snapshot` on each call to `Collect()`. Rates are computed by dividing the counter delta by the elapsed seconds since the previous scrape.

- [ ] **Step 1: Write the failing test**

```go
// internal/collector/collector_test.go
package collector_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kube-nat/kube-nat/internal/collector"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestCollectBuildsAgentSnap(t *testing.T) {
	// Fake agent metrics endpoint
	metricsBody := `
# HELP kube_nat_bytes_tx_total Total bytes transmitted
# TYPE kube_nat_bytes_tx_total counter
kube_nat_bytes_tx_total{az="eu-west-1a",instance_id="i-0abc",iface="eth0"} 1000000
# HELP kube_nat_bytes_rx_total Total bytes received
# TYPE kube_nat_bytes_rx_total counter
kube_nat_bytes_rx_total{az="eu-west-1a",instance_id="i-0abc",iface="eth0"} 500000
# HELP kube_nat_conntrack_entries Current conntrack entries
# TYPE kube_nat_conntrack_entries gauge
kube_nat_conntrack_entries 12345
# HELP kube_nat_conntrack_max Max conntrack entries
# TYPE kube_nat_conntrack_max gauge
kube_nat_conntrack_max 262144
# HELP kube_nat_conntrack_usage_ratio Conntrack ratio
# TYPE kube_nat_conntrack_usage_ratio gauge
kube_nat_conntrack_usage_ratio 0.047
# HELP kube_nat_rule_present iptables rule present
# TYPE kube_nat_rule_present gauge
kube_nat_rule_present{rule="MASQUERADE"} 1
# HELP kube_nat_src_dst_check_disabled src/dst check
# TYPE kube_nat_src_dst_check_disabled gauge
kube_nat_src_dst_check_disabled 1
# HELP kube_nat_route_table_owned route table owned
# TYPE kube_nat_route_table_owned gauge
kube_nat_route_table_owned{rtb_id="rtb-001"} 1
# HELP kube_nat_spot_interruption_pending spot pending
# TYPE kube_nat_spot_interruption_pending gauge
kube_nat_spot_interruption_pending 0
# HELP kube_nat_last_failover_seconds last failover
# TYPE kube_nat_last_failover_seconds gauge
kube_nat_last_failover_seconds{az="eu-west-1a"} 0
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.Write([]byte(metricsBody))
	}))
	defer srv.Close()

	// Seed fake k8s with one agent pod pointing at our test server
	host := srv.Listener.Addr().String()
	// Extract just the IP (httptest uses 127.0.0.1:<port>)
	k8s := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-nat-agent-abc",
			Namespace: "kube-system",
			Labels:    map[string]string{"app": "kube-nat", "component": "agent"},
		},
		Status: corev1.PodStatus{PodIP: "127.0.0.1"},
	})

	c := collector.New(collector.Config{
		K8sClient:      k8s,
		Namespace:      "kube-system",
		MetricsPort:    extractPort(host),
		ScrapeInterval: 5 * time.Second,
	})

	snap, err := c.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Agents) != 1 {
		t.Fatalf("want 1 agent got %d", len(snap.Agents))
	}
	a := snap.Agents[0]
	if a.AZ != "eu-west-1a" {
		t.Errorf("want az=eu-west-1a got %q", a.AZ)
	}
	if a.ConntrackEntries != 12345 {
		t.Errorf("want conntrack=12345 got %v", a.ConntrackEntries)
	}
	if !a.RulePresent {
		t.Error("expected rule_present=true")
	}
	if !a.SrcDstDisabled {
		t.Error("expected src_dst_disabled=true")
	}
	if len(a.RouteTablesOwned) != 1 || a.RouteTablesOwned[0] != "rtb-001" {
		t.Errorf("expected rtb-001, got %v", a.RouteTablesOwned)
	}
}

func extractPort(addr string) int {
	// addr is "127.0.0.1:PORT"
	var port int
	fmt.Sscanf(addr, "127.0.0.1:%d", &port)
	return port
}
```

Add `"fmt"` to the import block.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/collector/... -run TestCollectBuildsAgentSnap`
Expected: FAIL — `collector.New` not defined yet

- [ ] **Step 3: Implement collector.go**

```go
// internal/collector/collector.go
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
		agents    []AgentSnap
		totalTx   float64
		totalRx   float64
		failovers []FailoverEvent
	)

	for _, pod := range pods.Items {
		if pod.Status.PodIP == "" {
			continue
		}
		url := fmt.Sprintf("http://%s:%d/metrics", pod.Status.PodIP, c.cfg.MetricsPort)
		families, err := c.scrape(ctx, url)
		if err != nil {
			continue // skip unreachable agents; they'll show as missing from agents list
		}

		snap := c.buildSnap(pod.Status.PodIP, families)
		if snap == nil {
			continue
		}

		// Derive failover events from last_failover_seconds changes.
		c.mu.Lock()
		foKey := snap.AZ
		if prev, ok := c.seen[foKey]; ok && snap.LastFailoverTS > 0 && snap.LastFailoverTS != prev {
			failovers = append(failovers, FailoverEvent{
				FromAZ: snap.AZ,
				ToAZ:   snap.AZ, // direction unknown from metric alone; label shows the AZ that was taken over
				TS:     snap.LastFailoverTS,
			})
		}
		if snap.LastFailoverTS > 0 {
			c.seen[foKey] = snap.LastFailoverTS
		}
		c.mu.Unlock()

		agents = append(agents, *snap)
		totalTx += snap.TxBytesPerSec
		totalRx += snap.RxBytesPerSec
	}

	// Append to history ring buffer (keep last 60 points).
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

// scrape fetches and parses Prometheus text metrics from a URL.
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
	var parser expfmt.TextParser
	families, err := parser.TextToMetricFamilies(strings.NewReader(string(body)))
	return families, err
}

// buildSnap extracts an AgentSnap from parsed metric families.
func (c *Collector) buildSnap(podIP string, families map[string]*dto.MetricFamily) *AgentSnap {
	snap := &AgentSnap{}

	// kube_nat_conntrack_entries / max / ratio
	snap.ConntrackEntries = gaugeVal(families, "kube_nat_conntrack_entries")
	snap.ConntrackMax = gaugeVal(families, "kube_nat_conntrack_max")
	snap.ConntrackRatio = gaugeVal(families, "kube_nat_conntrack_usage_ratio")
	snap.RulePresent = gaugeVal(families, "kube_nat_rule_present") >= 1
	snap.SrcDstDisabled = gaugeVal(families, "kube_nat_src_dst_check_disabled") >= 1
	snap.SpotPending = gaugeVal(families, "kube_nat_spot_interruption_pending") >= 1

	// kube_nat_route_table_owned{rtb_id=...} — collect all with value>=1
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

	// kube_nat_bytes_tx_total / rx — read AZ + instance_id labels and compute rate
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

	// Rate calculation by diffing against previous scrape.
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

	// kube_nat_last_failover_seconds{az=...}
	if mf, ok := families["kube_nat_last_failover_seconds"]; ok {
		for _, m := range mf.Metric {
			for _, lp := range m.Label {
				if lp.GetName() == "az" && lp.GetValue() == snap.AZ {
					if m.Gauge != nil {
						snap.LastFailoverTS = m.Gauge.GetValue()
					}
				}
			}
		}
	}

	// kube_nat_peer_status: any peer with value=1 means peers exist and are up
	if mf, ok := families["kube_nat_peer_status"]; ok {
		for _, m := range mf.Metric {
			if m.Gauge != nil && m.Gauge.GetValue() >= 1 {
				snap.PeerUp = true
				break
			}
		}
	}

	if snap.AZ == "" {
		return nil // no useful data
	}
	return snap
}

// gaugeVal returns the first gauge value for a metric family, or 0.
func gaugeVal(families map[string]*dto.MetricFamily, name string) float64 {
	mf, ok := families[name]
	if !ok || len(mf.Metric) == 0 {
		return 0
	}
	m := mf.Metric[0]
	if m.Gauge != nil {
		return m.Gauge.GetValue()
	}
	return 0
}
```

- [ ] **Step 4: Add missing import to test**

In `collector_test.go`, ensure `"fmt"` is in the import block.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/collector/... -v -run TestCollectBuildsAgentSnap`
Expected: PASS

- [ ] **Step 6: Run full suite**

Run: `go test ./... 2>&1 | tail -20`
Expected: all packages pass

- [ ] **Step 7: Commit**

```bash
git add internal/collector/
git commit -m "feat: add collector — k8s pod discovery and Prometheus metrics scraping"
```

---

### Task 4: WebSocket hub

**Files:**
- Create: `internal/dashboard/hub.go`

The hub registers/unregisters WebSocket clients and fans out `[]byte` payloads to all of them. If a write fails (client disconnected), the client is removed.

- [ ] **Step 1: Add coder/websocket dependency**

Run:
```bash
go get coder.com/coder/websocket@latest 2>/dev/null || go get nhooyr.io/websocket@latest
go mod tidy
```

Check which resolved:
```bash
grep -E "nhooyr|coder.*websocket" go.mod
```

The import path to use in code is whichever appears in `go.mod` (typically `nhooyr.io/websocket`).

- [ ] **Step 2: Create hub.go**

Use the import path resolved in Step 1. If it resolved as `nhooyr.io/websocket`, use that. If it resolved as `coder.com/coder/websocket`, use that.

```go
// internal/dashboard/hub.go
package dashboard

import (
	"context"
	"sync"

	"nhooyr.io/websocket"
)

// client wraps a single browser WebSocket connection.
type client struct {
	conn *websocket.Conn
}

// Hub manages connected browser clients and broadcasts snapshots to them.
type Hub struct {
	mu      sync.Mutex
	clients map[*client]struct{}
}

// NewHub creates a Hub.
func NewHub() *Hub {
	return &Hub{clients: make(map[*client]struct{})}
}

// Register adds a client to the hub.
func (h *Hub) Register(c *client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

// Unregister removes a client from the hub.
func (h *Hub) Unregister(c *client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

// Broadcast sends payload to all connected clients.
// Clients that fail to receive are removed.
func (h *Hub) Broadcast(ctx context.Context, payload []byte) {
	h.mu.Lock()
	clients := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()

	for _, c := range clients {
		if err := c.conn.Write(ctx, websocket.MessageText, payload); err != nil {
			h.Unregister(c)
			c.conn.Close(websocket.StatusGoingAway, "write error")
		}
	}
}
```

- [ ] **Step 3: Verify it compiles**

Run: `go build ./internal/dashboard/...`
Expected: no output

- [ ] **Step 4: Commit**

```bash
git add internal/dashboard/hub.go go.mod go.sum
git commit -m "feat: add WebSocket hub for dashboard client broadcast"
```

---

### Task 5: Dashboard HTTP server

**Files:**
- Create: `internal/dashboard/server.go`
- Create: `internal/dashboard/server_test.go`
- Create: `web/dist/index.html` (placeholder for embed)

The server embeds `web/dist`, serves it at `/`, upgrades `/ws` to WebSocket, and runs the collector loop — pushing a new Snapshot JSON to all clients every `ScrapeInterval`.

- [ ] **Step 1: Create the embed placeholder**

```bash
mkdir -p web/dist
```

Write `web/dist/index.html`:
```html
<!doctype html>
<html><head><title>kube-nat dashboard</title></head>
<body><p>Run <code>make build-web</code> to build the React app.</p></body>
</html>
```

- [ ] **Step 2: Write the failing test**

```go
// internal/dashboard/server_test.go
package dashboard_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kube-nat/kube-nat/internal/collector"
	"github.com/kube-nat/kube-nat/internal/dashboard"
	"k8s.io/client-go/kubernetes/fake"
)

func TestHealthzReturns200(t *testing.T) {
	k8s := fake.NewSimpleClientset()
	srv := dashboard.NewServer(dashboard.Config{
		K8sClient:    k8s,
		Namespace:    "kube-system",
		MetricsPort:  9100,
		ScrapeInterval: 5,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200 got %d", resp.StatusCode)
	}
}

func TestRootServesSPA(t *testing.T) {
	k8s := fake.NewSimpleClientset()
	srv := dashboard.NewServer(dashboard.Config{
		K8sClient:    k8s,
		Namespace:    "kube-system",
		MetricsPort:  9100,
		ScrapeInterval: 5,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200 got %d", resp.StatusCode)
	}
}

// Compile-time check that dashboard.Config has the right fields.
var _ = collector.Config{}
var _ = context.Background
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/dashboard/... -run TestHealthzReturns200`
Expected: FAIL — `dashboard.NewServer` not defined

- [ ] **Step 4: Implement server.go**

```go
// internal/dashboard/server.go
package dashboard

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/kube-nat/kube-nat/internal/collector"
	"nhooyr.io/websocket"
	"k8s.io/client-go/kubernetes"
)

//go:embed ../../web/dist
var staticFiles embed.FS

// Config configures the dashboard server.
type Config struct {
	K8sClient      kubernetes.Interface
	Namespace      string
	MetricsPort    int
	ScrapeInterval int // seconds
}

// Server is the dashboard HTTP server.
type Server struct {
	cfg       Config
	hub       *Hub
	collector *collector.Collector
	logger    *log.Logger
}

// NewServer creates a Server.
func NewServer(cfg Config) *Server {
	col := collector.New(collector.Config{
		K8sClient:      cfg.K8sClient,
		Namespace:      cfg.Namespace,
		MetricsPort:    cfg.MetricsPort,
		ScrapeInterval: time.Duration(cfg.ScrapeInterval) * time.Second,
	})
	return &Server{
		cfg:       cfg,
		hub:       NewHub(),
		collector: col,
		logger:    log.New(os.Stderr, "[dashboard] ", log.LstdFlags),
	}
}

// Handler returns the HTTP handler (useful for testing without starting the server).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Serve embedded SPA. Sub into web/dist so paths like /assets/... work.
	sub, err := fs.Sub(staticFiles, "web/dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	mux.Handle("/", fileServer)

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/ws", s.handleWS)

	return mux
}

// Run starts the collector loop and HTTP server. Blocks until ctx is done.
func (s *Server) Run(ctx context.Context, addr string) error {
	// Start collector loop — push Snapshot to all WS clients every ScrapeInterval.
	go func() {
		interval := time.Duration(s.cfg.ScrapeInterval) * time.Second
		if interval == 0 {
			interval = 5 * time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				snap, err := s.collector.Collect(ctx)
				if err != nil {
					s.logger.Printf("collect error: %v", err)
					continue
				}
				b, err := json.Marshal(snap)
				if err != nil {
					s.logger.Printf("marshal error: %v", err)
					continue
				}
				s.hub.Broadcast(ctx, b)
			}
		}
	}()

	srv := &http.Server{Addr: addr, Handler: s.Handler()}
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()
	s.logger.Printf("listening on %s", addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// handleWS upgrades to WebSocket and registers the client with the hub.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // allow any Origin for kubectl port-forward use
	})
	if err != nil {
		s.logger.Printf("ws accept: %v", err)
		return
	}
	c := &client{conn: conn}
	s.hub.Register(c)
	defer s.hub.Unregister(c)

	// Send current snapshot immediately so the browser doesn't wait for the first tick.
	snap, err := s.collector.Collect(r.Context())
	if err == nil {
		if b, err := json.Marshal(snap); err == nil {
			conn.Write(r.Context(), websocket.MessageText, b)
		}
	}

	// Block until client disconnects.
	for {
		if _, _, err := conn.Read(r.Context()); err != nil {
			return
		}
	}
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/dashboard/... -v`
Expected: both tests PASS

- [ ] **Step 6: Commit**

```bash
git add internal/dashboard/server.go internal/dashboard/server_test.go web/dist/index.html
git commit -m "feat: add dashboard HTTP server with WebSocket broadcast and embedded SPA"
```

---

### Task 6: Wire dashboard subcommand into main.go

**Files:**
- Modify: `cmd/kube-nat/main.go`

- [ ] **Step 1: Read current main.go**

Run: `cat cmd/kube-nat/main.go`

- [ ] **Step 2: Add dashboardCmd()**

Replace `cmd/kube-nat/main.go` entirely:
```go
package main

import (
	"fmt"
	"os"

	"github.com/kube-nat/kube-nat/internal/agent"
	"github.com/kube-nat/kube-nat/internal/config"
	"github.com/kube-nat/kube-nat/internal/dashboard"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func main() {
	root := &cobra.Command{
		Use:   "kube-nat",
		Short: "Kubernetes-native NAT for AWS",
	}
	root.AddCommand(agentCmd())
	root.AddCommand(dashboardCmd())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func agentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agent",
		Short: "Run the NAT agent (DaemonSet mode)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			return agent.Run(cfg)
		},
	}
}

func dashboardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dashboard",
		Short: "Run the real-time NAT dashboard",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			k8sCfg, err := rest.InClusterConfig()
			if err != nil {
				return fmt.Errorf("k8s in-cluster config: %w", err)
			}
			k8sClient, err := kubernetes.NewForConfig(k8sCfg)
			if err != nil {
				return fmt.Errorf("k8s client: %w", err)
			}
			srv := dashboard.NewServer(dashboard.Config{
				K8sClient:      k8sClient,
				Namespace:      cfg.Namespace,
				MetricsPort:    cfg.MetricsPort,
				ScrapeInterval: int(cfg.ScrapeInterval.Seconds()),
			})
			return srv.Run(cmd.Context(), fmt.Sprintf(":%d", cfg.DashboardPort))
		},
	}
}
```

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: no output

- [ ] **Step 4: Commit**

```bash
git add cmd/kube-nat/main.go
git commit -m "feat: add dashboard subcommand to kube-nat binary"
```

---

### Task 7: React SPA scaffold

**Files:**
- Create: `web/package.json`
- Create: `web/tsconfig.json`
- Create: `web/vite.config.ts`
- Create: `web/index.html`
- Create: `web/src/types.ts`
- Create: `web/src/main.tsx`

- [ ] **Step 1: Create package.json**

```json
{
  "name": "kube-nat-dashboard",
  "version": "1.0.0",
  "private": true,
  "scripts": {
    "dev": "vite",
    "build": "tsc --noEmit && vite build",
    "preview": "vite preview"
  },
  "dependencies": {
    "react": "^18.2.0",
    "react-dom": "^18.2.0",
    "recharts": "^2.12.7"
  },
  "devDependencies": {
    "@types/react": "^18.2.0",
    "@types/react-dom": "^18.2.0",
    "@vitejs/plugin-react": "^4.2.0",
    "autoprefixer": "^10.4.19",
    "postcss": "^8.4.38",
    "tailwindcss": "^3.4.4",
    "typescript": "^5.4.5",
    "vite": "^5.2.0"
  }
}
```

- [ ] **Step 2: Create tsconfig.json**

```json
{
  "compilerOptions": {
    "target": "ES2020",
    "useDefineForClassFields": true,
    "lib": ["ES2020", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "skipLibCheck": true,
    "moduleResolution": "bundler",
    "allowImportingTsExtensions": true,
    "resolveJsonModule": true,
    "isolatedModules": true,
    "noEmit": true,
    "jsx": "react-jsx",
    "strict": true
  },
  "include": ["src"]
}
```

- [ ] **Step 3: Create vite.config.ts**

```ts
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/ws': { target: 'ws://localhost:8080', ws: true },
      '/healthz': 'http://localhost:8080',
    },
  },
})
```

- [ ] **Step 4: Create index.html**

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>kube-nat dashboard</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

- [ ] **Step 5: Create src/types.ts**

These types mirror the Go `Snapshot` JSON exactly.

```ts
export interface AgentSnap {
  az: string
  instance_id: string
  tx_bps: number       // bytes per second
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
  ts: number   // unix milliseconds
  tx: number   // bytes per second
  rx: number
}

export interface FailoverEvent {
  from_az: string
  to_az: string
  ts: number   // unix seconds
}

export interface Snapshot {
  ts: string         // ISO timestamp
  agents: AgentSnap[]
  history: HistoryPoint[]
  failovers: FailoverEvent[]
}
```

- [ ] **Step 6: Create src/main.tsx**

```tsx
import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import './index.css'

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
)
```

- [ ] **Step 7: Create src/index.css**

```css
@tailwind base;
@tailwind components;
@tailwind utilities;

body {
  @apply bg-gray-950 text-gray-100 font-mono;
}
```

- [ ] **Step 8: Create tailwind.config.js and postcss.config.js**

`web/tailwind.config.js`:
```js
/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: { extend: {} },
  plugins: [],
}
```

`web/postcss.config.js`:
```js
export default {
  plugins: {
    tailwindcss: {},
    autoprefixer: {},
  },
}
```

- [ ] **Step 9: Install dependencies**

Run: `cd web && npm install`
Expected: `node_modules/` created, no errors

- [ ] **Step 10: Commit**

```bash
git add web/
git commit -m "feat: scaffold React SPA with Vite, TypeScript, Tailwind, recharts"
```

---

### Task 8: WebSocket hook + App shell

**Files:**
- Create: `web/src/hooks/useWebSocket.ts`
- Create: `web/src/App.tsx`

- [ ] **Step 1: Create useWebSocket.ts**

The hook connects to `/ws`, reconnects on disconnect, and returns the latest Snapshot.

```ts
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
```

- [ ] **Step 2: Create App.tsx**

```tsx
import { useWebSocket } from './hooks/useWebSocket'
import { Header } from './components/Header'
import { SummaryCards } from './components/SummaryCards'
import { AZCard } from './components/AZCard'
import { BandwidthChart } from './components/BandwidthChart'
import { FailoverLog } from './components/FailoverLog'

export default function App() {
  const snap = useWebSocket()

  if (!snap) {
    return (
      <div className="flex items-center justify-center h-screen text-gray-400">
        Connecting to kube-nat dashboard…
      </div>
    )
  }

  return (
    <div className="min-h-screen p-4 space-y-6 max-w-7xl mx-auto">
      <Header agents={snap.agents} />
      <SummaryCards agents={snap.agents} failovers={snap.failovers} />
      <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
        {snap.agents.map((a) => (
          <AZCard key={a.az} agent={a} />
        ))}
      </div>
      <BandwidthChart history={snap.history} />
      <FailoverLog failovers={snap.failovers} />
    </div>
  )
}
```

- [ ] **Step 3: Commit**

```bash
git add web/src/hooks/ web/src/App.tsx
git commit -m "feat: add WebSocket hook and App shell"
```

---

### Task 9: Header + SummaryCards

**Files:**
- Create: `web/src/components/Header.tsx`
- Create: `web/src/components/SummaryCards.tsx`

- [ ] **Step 1: Create Header.tsx**

```tsx
import type { AgentSnap } from '../types'

interface Props {
  agents: AgentSnap[]
}

export function Header({ agents }: Props) {
  const healthy = agents.filter((a) => a.rule_present && a.src_dst_disabled).length
  const status = healthy === agents.length && agents.length > 0 ? 'Healthy' : 'Degraded'
  const statusColor = status === 'Healthy' ? 'text-green-400' : 'text-red-400'

  return (
    <header className="flex items-center justify-between border-b border-gray-800 pb-4">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">kube-nat</h1>
        <p className="text-gray-400 text-sm">Real-time NAT dashboard</p>
      </div>
      <div className="text-right">
        <div className={`text-lg font-semibold ${statusColor}`}>{status}</div>
        <div className="text-gray-400 text-sm">
          {agents.length} node{agents.length !== 1 ? 's' : ''} · {new Set(agents.map((a) => a.az)).size} AZ{new Set(agents.map((a) => a.az)).size !== 1 ? 's' : ''}
        </div>
      </div>
    </header>
  )
}
```

- [ ] **Step 2: Create SummaryCards.tsx**

```tsx
import type { AgentSnap, FailoverEvent } from '../types'

function fmtBps(bps: number): string {
  if (bps >= 1e9) return `${(bps / 1e9).toFixed(1)} GB/s`
  if (bps >= 1e6) return `${(bps / 1e6).toFixed(1)} MB/s`
  if (bps >= 1e3) return `${(bps / 1e3).toFixed(1)} KB/s`
  return `${bps.toFixed(0)} B/s`
}

interface Props {
  agents: AgentSnap[]
  failovers: FailoverEvent[]
}

export function SummaryCards({ agents, failovers }: Props) {
  const totalTx = agents.reduce((s, a) => s + a.tx_bps, 0)
  const totalRx = agents.reduce((s, a) => s + a.rx_bps, 0)
  const totalConn = agents.reduce((s, a) => s + a.conntrack_entries, 0)
  const maxConn = agents.reduce((s, a) => s + a.conntrack_max, 0)
  const connRatio = maxConn > 0 ? totalConn / maxConn : 0
  const fo24h = failovers.filter((f) => f.ts > Date.now() / 1000 - 86400).length

  return (
    <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
      <Card label="TX" value={fmtBps(totalTx)} />
      <Card label="RX" value={fmtBps(totalRx)} />
      <Card label="Connections" value={totalConn.toLocaleString()}>
        <div className="mt-2 h-1.5 bg-gray-800 rounded">
          <div
            className={`h-full rounded ${connRatio > 0.7 ? 'bg-red-500' : 'bg-green-500'}`}
            style={{ width: `${Math.min(connRatio * 100, 100).toFixed(1)}%` }}
          />
        </div>
        <div className="text-xs text-gray-500 mt-1">{(connRatio * 100).toFixed(1)}% of limit</div>
      </Card>
      <Card label="Failovers (24h)" value={String(fo24h)} />
    </div>
  )
}

function Card({ label, value, children }: { label: string; value: string; children?: React.ReactNode }) {
  return (
    <div className="bg-gray-900 border border-gray-800 rounded-lg p-4">
      <div className="text-gray-400 text-xs uppercase tracking-widest mb-1">{label}</div>
      <div className="text-2xl font-bold">{value}</div>
      {children}
    </div>
  )
}
```

- [ ] **Step 3: Add React import to SummaryCards.tsx**

`SummaryCards.tsx` uses `React.ReactNode` — add `import React from 'react'` at the top, or use `import type { ReactNode } from 'react'` and change the type accordingly.

Replace the function Card signature to use `ReactNode`:
```tsx
import type { ReactNode } from 'react'
// ...
function Card({ label, value, children }: { label: string; value: string; children?: ReactNode }) {
```

- [ ] **Step 4: Commit**

```bash
git add web/src/components/Header.tsx web/src/components/SummaryCards.tsx
git commit -m "feat: add Header and SummaryCards components"
```

---

### Task 10: AZ card

**Files:**
- Create: `web/src/components/AZCard.tsx`

- [ ] **Step 1: Create AZCard.tsx**

```tsx
import type { AgentSnap } from '../types'

interface Props {
  agent: AgentSnap
}

function fmtBps(bps: number): string {
  if (bps >= 1e6) return `${(bps / 1e6).toFixed(1)} MB/s`
  if (bps >= 1e3) return `${(bps / 1e3).toFixed(1)} KB/s`
  return `${bps.toFixed(0)} B/s`
}

export function AZCard({ agent: a }: Props) {
  const statusDot = a.rule_present && a.src_dst_disabled
    ? 'bg-green-400'
    : 'bg-red-400'

  const connPct = a.conntrack_max > 0
    ? (a.conntrack_entries / a.conntrack_max) * 100
    : 0
  const connBarColor = connPct > 70 ? 'bg-red-500' : connPct > 50 ? 'bg-yellow-400' : 'bg-green-500'

  return (
    <div className="bg-gray-900 border border-gray-800 rounded-lg p-4 space-y-3">
      {/* Header row */}
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

      {/* Instance */}
      <div className="text-xs text-gray-400">{a.instance_id || '—'}</div>

      {/* Throughput */}
      <div className="flex gap-4 text-sm">
        <div><span className="text-gray-400">TX </span><span>{fmtBps(a.tx_bps)}</span></div>
        <div><span className="text-gray-400">RX </span><span>{fmtBps(a.rx_bps)}</span></div>
      </div>

      {/* Conntrack bar */}
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

      {/* Route tables */}
      {a.route_tables?.length > 0 && (
        <div className="text-xs text-gray-400">
          Routes: {a.route_tables.join(', ')}
        </div>
      )}

      {/* Health flags */}
      <div className="flex gap-2 text-xs">
        <Flag ok={a.rule_present} label="iptables" />
        <Flag ok={a.src_dst_disabled} label="src/dst" />
        <Flag ok={a.peer_up} label="peer" />
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
```

- [ ] **Step 2: Commit**

```bash
git add web/src/components/AZCard.tsx
git commit -m "feat: add per-AZ card with conntrack bar and health flags"
```

---

### Task 11: Bandwidth sparkline

**Files:**
- Create: `web/src/components/BandwidthChart.tsx`

Uses recharts `AreaChart` to show aggregate TX/RX over the last 5 minutes (60 × 5s samples).

- [ ] **Step 1: Create BandwidthChart.tsx**

```tsx
import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  CartesianGrid,
} from 'recharts'
import type { HistoryPoint } from '../types'

interface Props {
  history: HistoryPoint[]
}

function fmtBytes(bps: number): string {
  if (bps >= 1e6) return `${(bps / 1e6).toFixed(1)}M`
  if (bps >= 1e3) return `${(bps / 1e3).toFixed(0)}K`
  return String(bps.toFixed(0))
}

export function BandwidthChart({ history }: Props) {
  const data = history.map((p) => ({
    t: new Date(p.ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' }),
    tx: p.tx,
    rx: p.rx,
  }))

  return (
    <div className="bg-gray-900 border border-gray-800 rounded-lg p-4">
      <div className="text-gray-400 text-xs uppercase tracking-widest mb-3">
        Bandwidth — last 5 min
      </div>
      <ResponsiveContainer width="100%" height={160}>
        <AreaChart data={data} margin={{ top: 4, right: 8, left: 0, bottom: 0 }}>
          <defs>
            <linearGradient id="tx" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor="#34d399" stopOpacity={0.3} />
              <stop offset="95%" stopColor="#34d399" stopOpacity={0} />
            </linearGradient>
            <linearGradient id="rx" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor="#60a5fa" stopOpacity={0.3} />
              <stop offset="95%" stopColor="#60a5fa" stopOpacity={0} />
            </linearGradient>
          </defs>
          <CartesianGrid strokeDasharray="3 3" stroke="#1f2937" />
          <XAxis dataKey="t" tick={{ fill: '#6b7280', fontSize: 10 }} interval="preserveStartEnd" />
          <YAxis tickFormatter={fmtBytes} tick={{ fill: '#6b7280', fontSize: 10 }} width={40} />
          <Tooltip
            contentStyle={{ background: '#111827', border: '1px solid #374151', fontSize: 12 }}
            formatter={(v: number, name: string) => [`${fmtBytes(v)} B/s`, name === 'tx' ? 'TX' : 'RX']}
          />
          <Area type="monotone" dataKey="tx" stroke="#34d399" fill="url(#tx)" strokeWidth={1.5} dot={false} />
          <Area type="monotone" dataKey="rx" stroke="#60a5fa" fill="url(#rx)" strokeWidth={1.5} dot={false} />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  )
}
```

- [ ] **Step 2: Commit**

```bash
git add web/src/components/BandwidthChart.tsx
git commit -m "feat: add bandwidth sparkline using recharts"
```

---

### Task 12: Failover event log

**Files:**
- Create: `web/src/components/FailoverLog.tsx`

- [ ] **Step 1: Create FailoverLog.tsx**

```tsx
import type { FailoverEvent } from '../types'

interface Props {
  failovers: FailoverEvent[]
}

export function FailoverLog({ failovers }: Props) {
  const sorted = [...failovers].sort((a, b) => b.ts - a.ts).slice(0, 20)

  return (
    <div className="bg-gray-900 border border-gray-800 rounded-lg p-4">
      <div className="text-gray-400 text-xs uppercase tracking-widest mb-3">
        Failover events
      </div>
      {sorted.length === 0 ? (
        <div className="text-gray-600 text-sm">No failover events.</div>
      ) : (
        <table className="w-full text-sm">
          <thead>
            <tr className="text-gray-500 text-xs border-b border-gray-800">
              <th className="text-left py-1">Time</th>
              <th className="text-left py-1">From AZ</th>
              <th className="text-left py-1">Covered by</th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((f, i) => (
              <tr key={i} className="border-b border-gray-800/50">
                <td className="py-1 text-gray-400">
                  {new Date(f.ts * 1000).toLocaleString()}
                </td>
                <td className="py-1">{f.from_az}</td>
                <td className="py-1 text-green-400">{f.to_az || '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}
```

- [ ] **Step 2: Verify the full SPA builds**

Run:
```bash
cd web && npm run build 2>&1 | tail -20
```
Expected: `dist/index.html` and `dist/assets/` created, no TypeScript errors.

- [ ] **Step 3: Verify Go still builds with the new dist**

Run: `go build ./...`
Expected: no output

- [ ] **Step 4: Commit**

```bash
git add web/src/components/FailoverLog.tsx web/dist/
git commit -m "feat: add failover event log and include compiled SPA in repo"
```

---

### Task 13: Update Dockerfile for Node build stage

**Files:**
- Modify: `Dockerfile`

- [ ] **Step 1: Read current Dockerfile**

Run: `cat Dockerfile`

- [ ] **Step 2: Replace with multi-stage build (Node → Go → runtime)**

```dockerfile
# Stage 1: Build the React SPA
FROM node:20-bookworm-slim AS web-builder
WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN npm ci --prefer-offline
COPY web/ ./
RUN npm run build

# Stage 2: Build the Go binary
FROM golang:1.22-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-builder /web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /kube-nat ./cmd/kube-nat

# Stage 3: Minimal runtime image
FROM debian:bookworm-slim AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends \
    iptables \
    conntrack \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /kube-nat /usr/local/bin/kube-nat
ENTRYPOINT ["/usr/local/bin/kube-nat"]
CMD ["agent"]
```

- [ ] **Step 3: Commit**

```bash
git add Dockerfile
git commit -m "feat: add Node build stage to Dockerfile for SPA"
```

---

### Task 14: Kubernetes manifests

**Files:**
- Create: `deploy/manifests/namespace.yaml`
- Create: `deploy/manifests/priorityclass.yaml`
- Create: `deploy/manifests/serviceaccount.yaml`
- Create: `deploy/manifests/rbac.yaml`
- Create: `deploy/manifests/daemonset.yaml`
- Create: `deploy/manifests/deployment-dashboard.yaml`
- Create: `deploy/manifests/service.yaml`
- Create: `deploy/manifests/pdb.yaml`
- Create: `deploy/manifests/networkpolicy.yaml`

- [ ] **Step 1: Create namespace.yaml**

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: kube-nat
  labels:
    app.kubernetes.io/name: kube-nat
```

- [ ] **Step 2: Create priorityclass.yaml**

```yaml
apiVersion: scheduling.k8s.io/v1
kind: PriorityClass
metadata:
  name: system-node-critical
value: 2000001000
globalDefault: false
description: "kube-nat NAT agent — survives node memory pressure eviction"
```

- [ ] **Step 3: Create serviceaccount.yaml**

```yaml
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: kube-nat-agent
  namespace: kube-nat
  annotations:
    eks.amazonaws.com/role-arn: ""   # set to your IRSA role ARN
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: kube-nat-dashboard
  namespace: kube-nat
```

- [ ] **Step 4: Create rbac.yaml**

```yaml
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kube-nat-agent
rules:
- apiGroups: ["coordination.k8s.io"]
  resources: ["leases"]
  verbs: ["get", "create", "update", "list", "watch"]
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["list", "watch"]
- apiGroups: [""]
  resources: ["nodes"]
  verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kube-nat-agent
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: kube-nat-agent
subjects:
- kind: ServiceAccount
  name: kube-nat-agent
  namespace: kube-nat
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kube-nat-dashboard
rules:
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["list", "watch"]
- apiGroups: ["coordination.k8s.io"]
  resources: ["leases"]
  verbs: ["list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kube-nat-dashboard
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: kube-nat-dashboard
subjects:
- kind: ServiceAccount
  name: kube-nat-dashboard
  namespace: kube-nat
```

- [ ] **Step 5: Create daemonset.yaml**

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: kube-nat-agent
  namespace: kube-nat
  labels:
    app: kube-nat
    component: agent
spec:
  selector:
    matchLabels:
      app: kube-nat
      component: agent
  template:
    metadata:
      labels:
        app: kube-nat
        component: agent
    spec:
      serviceAccountName: kube-nat-agent
      hostNetwork: true
      priorityClassName: system-node-critical
      terminationGracePeriodSeconds: 10
      nodeSelector:
        node-role.kubernetes.io/nat: "true"
      tolerations:
      - operator: Exists
        effect: NoSchedule
      containers:
      - name: agent
        image: ghcr.io/your-org/kube-nat:latest
        args: ["agent"]
        securityContext:
          privileged: false
          capabilities:
            add: ["NET_ADMIN", "NET_RAW"]
          readOnlyRootFilesystem: true
        resources:
          requests:
            cpu: 50m
            memory: 64Mi
          limits:
            cpu: 200m
            memory: 128Mi
        env:
        - name: POD_NAME
          valueFrom:
            fieldRef:
              fieldPath: metadata.name
        - name: KUBE_NAT_NAMESPACE
          valueFrom:
            fieldRef:
              fieldPath: metadata.namespace
        ports:
        - name: metrics
          containerPort: 9100
          protocol: TCP
        - name: peer
          containerPort: 9101
          protocol: TCP
        readinessProbe:
          httpGet:
            path: /readyz
            port: 9100
          initialDelaySeconds: 5
          periodSeconds: 5
        livenessProbe:
          httpGet:
            path: /healthz
            port: 9100
          initialDelaySeconds: 10
          periodSeconds: 10
        lifecycle:
          preStop:
            exec:
              command: ["/bin/sleep", "2"]
        volumeMounts:
        - name: proc-sys
          mountPath: /proc/sys
        - name: lib-modules
          mountPath: /lib/modules
          readOnly: true
      volumes:
      - name: proc-sys
        hostPath:
          path: /proc/sys
      - name: lib-modules
        hostPath:
          path: /lib/modules
```

- [ ] **Step 6: Create deployment-dashboard.yaml**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kube-nat-dashboard
  namespace: kube-nat
  labels:
    app: kube-nat
    component: dashboard
spec:
  replicas: 1
  selector:
    matchLabels:
      app: kube-nat
      component: dashboard
  template:
    metadata:
      labels:
        app: kube-nat
        component: dashboard
    spec:
      serviceAccountName: kube-nat-dashboard
      securityContext:
        runAsNonRoot: true
        runAsUser: 65534
        readOnlyRootFilesystem: true
      containers:
      - name: dashboard
        image: ghcr.io/your-org/kube-nat:latest
        args: ["dashboard"]
        securityContext:
          capabilities:
            drop: ["ALL"]
        resources:
          requests:
            cpu: 20m
            memory: 32Mi
          limits:
            cpu: 100m
            memory: 128Mi
        env:
        - name: KUBE_NAT_NAMESPACE
          value: kube-nat
        ports:
        - name: http
          containerPort: 8080
          protocol: TCP
        readinessProbe:
          httpGet:
            path: /healthz
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 5
```

- [ ] **Step 7: Create service.yaml**

```yaml
---
apiVersion: v1
kind: Service
metadata:
  name: kube-nat-agent
  namespace: kube-nat
  labels:
    app: kube-nat
    component: agent
spec:
  selector:
    app: kube-nat
    component: agent
  clusterIP: None          # headless — each pod is addressed directly by the collector
  ports:
  - name: metrics
    port: 9100
    targetPort: 9100
---
apiVersion: v1
kind: Service
metadata:
  name: kube-nat-dashboard
  namespace: kube-nat
  labels:
    app: kube-nat
    component: dashboard
spec:
  selector:
    app: kube-nat
    component: dashboard
  type: ClusterIP
  ports:
  - name: http
    port: 8080
    targetPort: 8080
```

- [ ] **Step 8: Create pdb.yaml**

```yaml
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: kube-nat-agent
  namespace: kube-nat
spec:
  maxUnavailable: 0
  selector:
    matchLabels:
      app: kube-nat
      component: agent
```

- [ ] **Step 9: Create networkpolicy.yaml**

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: kube-nat-agent
  namespace: kube-nat
spec:
  podSelector:
    matchLabels:
      app: kube-nat
      component: agent
  ingress:
  # metrics scraping from dashboard and Prometheus
  - ports:
    - port: 9100
      protocol: TCP
  # peer heartbeat from other kube-nat agents
  - from:
    - podSelector:
        matchLabels:
          app: kube-nat
          component: agent
    ports:
    - port: 9101
      protocol: TCP
  policyTypes:
  - Ingress
```

- [ ] **Step 10: Commit**

```bash
git add deploy/manifests/
git commit -m "feat: add Kubernetes manifests for agent DaemonSet, dashboard Deployment, RBAC, PDB, NetworkPolicy"
```

---

### Task 15: Helm chart

**Files:**
- Create: `deploy/helm/Chart.yaml`
- Create: `deploy/helm/values.yaml`
- Create: `deploy/helm/templates/_helpers.tpl`
- Create: `deploy/helm/templates/` (one file per manifest)

- [ ] **Step 1: Create Chart.yaml**

```yaml
apiVersion: v2
name: kube-nat
description: Kubernetes-native AWS NAT replacement
type: application
version: 0.1.0
appVersion: "0.1.0"
```

- [ ] **Step 2: Create values.yaml**

```yaml
image:
  repository: ghcr.io/your-org/kube-nat
  tag: latest
  pullPolicy: IfNotPresent

namespace: kube-nat

agent:
  irsaRoleArn: ""          # set to your IRSA IAM role ARN
  mode: auto               # "auto" or "manual"
  conntrackMax: ""         # override nf_conntrack_max (empty = kernel default)
  tagPrefix: kube-nat
  nodeSelector:
    node-role.kubernetes.io/nat: "true"
  resources:
    requests:
      cpu: 50m
      memory: 64Mi
    limits:
      cpu: 200m
      memory: 128Mi

dashboard:
  replicas: 1
  scrapeInterval: 5s
  port: 8080
  resources:
    requests:
      cpu: 20m
      memory: 32Mi
    limits:
      cpu: 100m
      memory: 128Mi

serviceMonitor:
  enabled: false           # set true if Prometheus Operator is installed
  interval: 30s
```

- [ ] **Step 3: Create templates/_helpers.tpl**

```
{{/*
Expand the name of the chart.
*/}}
{{- define "kube-nat.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "kube-nat.labels" -}}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
app.kubernetes.io/name: {{ include "kube-nat.name" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}
```

- [ ] **Step 4: Create templates/namespace.yaml**

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: {{ .Values.namespace }}
  labels:
    {{ include "kube-nat.labels" . | nindent 4 }}
```

- [ ] **Step 5: Create templates/serviceaccount.yaml**

```yaml
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: kube-nat-agent
  namespace: {{ .Values.namespace }}
  labels:
    {{ include "kube-nat.labels" . | nindent 4 }}
  {{- if .Values.agent.irsaRoleArn }}
  annotations:
    eks.amazonaws.com/role-arn: {{ .Values.agent.irsaRoleArn | quote }}
  {{- end }}
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: kube-nat-dashboard
  namespace: {{ .Values.namespace }}
  labels:
    {{ include "kube-nat.labels" . | nindent 4 }}
```

- [ ] **Step 6: Create templates/rbac.yaml**

```yaml
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kube-nat-agent
  labels:
    {{ include "kube-nat.labels" . | nindent 4 }}
rules:
- apiGroups: ["coordination.k8s.io"]
  resources: ["leases"]
  verbs: ["get", "create", "update", "list", "watch"]
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["list", "watch"]
- apiGroups: [""]
  resources: ["nodes"]
  verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kube-nat-agent
  labels:
    {{ include "kube-nat.labels" . | nindent 4 }}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: kube-nat-agent
subjects:
- kind: ServiceAccount
  name: kube-nat-agent
  namespace: {{ .Values.namespace }}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kube-nat-dashboard
  labels:
    {{ include "kube-nat.labels" . | nindent 4 }}
rules:
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["list", "watch"]
- apiGroups: ["coordination.k8s.io"]
  resources: ["leases"]
  verbs: ["list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kube-nat-dashboard
  labels:
    {{ include "kube-nat.labels" . | nindent 4 }}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: kube-nat-dashboard
subjects:
- kind: ServiceAccount
  name: kube-nat-dashboard
  namespace: {{ .Values.namespace }}
```

- [ ] **Step 7: Create templates/priorityclass.yaml**

```yaml
apiVersion: scheduling.k8s.io/v1
kind: PriorityClass
metadata:
  name: system-node-critical
  labels:
    {{ include "kube-nat.labels" . | nindent 4 }}
value: 2000001000
globalDefault: false
description: "kube-nat NAT agent — survives node memory pressure eviction"
```

- [ ] **Step 8: Create templates/daemonset.yaml**

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: kube-nat-agent
  namespace: {{ .Values.namespace }}
  labels:
    {{ include "kube-nat.labels" . | nindent 4 }}
    app: kube-nat
    component: agent
spec:
  selector:
    matchLabels:
      app: kube-nat
      component: agent
  template:
    metadata:
      labels:
        app: kube-nat
        component: agent
        {{ include "kube-nat.labels" . | nindent 8 }}
    spec:
      serviceAccountName: kube-nat-agent
      hostNetwork: true
      priorityClassName: system-node-critical
      terminationGracePeriodSeconds: 10
      nodeSelector:
        {{- toYaml .Values.agent.nodeSelector | nindent 8 }}
      tolerations:
      - operator: Exists
        effect: NoSchedule
      containers:
      - name: agent
        image: "{{ .Values.image.repository }}:{{ .Values.image.tag }}"
        imagePullPolicy: {{ .Values.image.pullPolicy }}
        args: ["agent"]
        securityContext:
          privileged: false
          capabilities:
            add: ["NET_ADMIN", "NET_RAW"]
          readOnlyRootFilesystem: true
        resources:
          {{- toYaml .Values.agent.resources | nindent 10 }}
        env:
        - name: POD_NAME
          valueFrom:
            fieldRef:
              fieldPath: metadata.name
        - name: KUBE_NAT_NAMESPACE
          value: {{ .Values.namespace | quote }}
        - name: KUBE_NAT_MODE
          value: {{ .Values.agent.mode | quote }}
        - name: KUBE_NAT_TAG_PREFIX
          value: {{ .Values.agent.tagPrefix | quote }}
        {{- if .Values.agent.conntrackMax }}
        - name: KUBE_NAT_CONNTRACK_MAX
          value: {{ .Values.agent.conntrackMax | quote }}
        {{- end }}
        ports:
        - name: metrics
          containerPort: 9100
          protocol: TCP
        - name: peer
          containerPort: 9101
          protocol: TCP
        readinessProbe:
          httpGet:
            path: /readyz
            port: 9100
          initialDelaySeconds: 5
          periodSeconds: 5
        livenessProbe:
          httpGet:
            path: /healthz
            port: 9100
          initialDelaySeconds: 10
          periodSeconds: 10
        lifecycle:
          preStop:
            exec:
              command: ["/bin/sleep", "2"]
        volumeMounts:
        - name: proc-sys
          mountPath: /proc/sys
        - name: lib-modules
          mountPath: /lib/modules
          readOnly: true
      volumes:
      - name: proc-sys
        hostPath:
          path: /proc/sys
      - name: lib-modules
        hostPath:
          path: /lib/modules
```

- [ ] **Step 9: Create templates/deployment.yaml (dashboard)**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kube-nat-dashboard
  namespace: {{ .Values.namespace }}
  labels:
    {{ include "kube-nat.labels" . | nindent 4 }}
    app: kube-nat
    component: dashboard
spec:
  replicas: {{ .Values.dashboard.replicas }}
  selector:
    matchLabels:
      app: kube-nat
      component: dashboard
  template:
    metadata:
      labels:
        app: kube-nat
        component: dashboard
        {{ include "kube-nat.labels" . | nindent 8 }}
    spec:
      serviceAccountName: kube-nat-dashboard
      securityContext:
        runAsNonRoot: true
        runAsUser: 65534
        readOnlyRootFilesystem: true
      containers:
      - name: dashboard
        image: "{{ .Values.image.repository }}:{{ .Values.image.tag }}"
        imagePullPolicy: {{ .Values.image.pullPolicy }}
        args: ["dashboard"]
        securityContext:
          capabilities:
            drop: ["ALL"]
        resources:
          {{- toYaml .Values.dashboard.resources | nindent 10 }}
        env:
        - name: KUBE_NAT_NAMESPACE
          value: {{ .Values.namespace | quote }}
        - name: KUBE_NAT_SCRAPE_INTERVAL
          value: {{ .Values.dashboard.scrapeInterval | quote }}
        - name: KUBE_NAT_DASHBOARD_PORT
          value: {{ .Values.dashboard.port | quote }}
        ports:
        - name: http
          containerPort: {{ .Values.dashboard.port }}
          protocol: TCP
        readinessProbe:
          httpGet:
            path: /healthz
            port: {{ .Values.dashboard.port }}
          initialDelaySeconds: 5
          periodSeconds: 5
```

- [ ] **Step 10: Create templates/service.yaml**

```yaml
---
apiVersion: v1
kind: Service
metadata:
  name: kube-nat-agent
  namespace: {{ .Values.namespace }}
  labels:
    {{ include "kube-nat.labels" . | nindent 4 }}
    app: kube-nat
    component: agent
spec:
  selector:
    app: kube-nat
    component: agent
  clusterIP: None
  ports:
  - name: metrics
    port: 9100
    targetPort: 9100
---
apiVersion: v1
kind: Service
metadata:
  name: kube-nat-dashboard
  namespace: {{ .Values.namespace }}
  labels:
    {{ include "kube-nat.labels" . | nindent 4 }}
    app: kube-nat
    component: dashboard
spec:
  selector:
    app: kube-nat
    component: dashboard
  type: ClusterIP
  ports:
  - name: http
    port: 8080
    targetPort: {{ .Values.dashboard.port }}
```

- [ ] **Step 11: Create templates/pdb.yaml**

```yaml
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: kube-nat-agent
  namespace: {{ .Values.namespace }}
  labels:
    {{ include "kube-nat.labels" . | nindent 4 }}
spec:
  maxUnavailable: 0
  selector:
    matchLabels:
      app: kube-nat
      component: agent
```

- [ ] **Step 12: Create templates/networkpolicy.yaml**

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: kube-nat-agent
  namespace: {{ .Values.namespace }}
  labels:
    {{ include "kube-nat.labels" . | nindent 4 }}
spec:
  podSelector:
    matchLabels:
      app: kube-nat
      component: agent
  ingress:
  - ports:
    - port: 9100
      protocol: TCP
  - from:
    - podSelector:
        matchLabels:
          app: kube-nat
          component: agent
    ports:
    - port: 9101
      protocol: TCP
  policyTypes:
  - Ingress
```

- [ ] **Step 13: Lint the Helm chart**

Run: `helm lint deploy/helm/`
Expected: `1 chart(s) linted, 0 chart(s) failed`

If helm is not installed: `brew install helm`

- [ ] **Step 14: Commit**

```bash
git add deploy/
git commit -m "feat: add Kubernetes manifests and Helm chart"
```

---

### Task 16: Makefile targets + final verification

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Read current Makefile**

Run: `cat Makefile`

- [ ] **Step 2: Add build-web and deploy targets**

Add to Makefile:
```makefile
.PHONY: build-web build test lint deploy

build-web:
	cd web && npm ci && npm run build

build: build-web
	go build -o bin/kube-nat ./cmd/kube-nat

test:
	go test ./... -race

lint:
	go vet ./...

deploy:
	kubectl apply -f deploy/manifests/
```

- [ ] **Step 3: Run full Go test suite with race detector**

Run: `go test ./... -race 2>&1 | tail -20`
Expected: all packages PASS

- [ ] **Step 4: Run go vet**

Run: `go vet ./...`
Expected: no output

- [ ] **Step 5: Verify web build**

Run: `cd web && npm run build 2>&1 | tail -10`
Expected: `dist/index.html` created, no TypeScript errors

- [ ] **Step 6: Commit**

```bash
git add Makefile
git commit -m "feat: add build-web and deploy Makefile targets"
```

---

## Self-review

**Spec coverage:**
- ✅ Dashboard collector scrapes agent pods every 5s
- ✅ Aggregates bandwidth (rate), conntrack, failover, peer health
- ✅ WebSocket push to browser
- ✅ React SPA: global header, summary cards (TX/RX/connections/failovers), per-AZ cards with conntrack bar
- ✅ Bandwidth sparkline (last 5 min, 5s interval)
- ✅ Failover event log
- ✅ Spot interruption indicator on AZ card
- ✅ `/healthz` on dashboard
- ✅ ClusterIP Service for kubectl port-forward
- ✅ `kubectl port-forward` workflow (no Ingress by default)
- ✅ PDB maxUnavailable:0
- ✅ RBAC: agent (leases/pods/nodes), dashboard (pods/leases)
- ✅ Helm chart with values.yaml IRSA annotation
- ✅ NetworkPolicy: peer port restricted to kube-nat pods
- ✅ Multi-stage Dockerfile with Node build

**Placeholder scan:** All steps contain actual code or exact commands.

**Type consistency:**
- Go `Snapshot.Agents []AgentSnap` → TS `Snapshot.agents: AgentSnap[]` ✅
- `AgentSnap.TxBytesPerSec` (Go) → `tx_bps` (JSON tag) → `AgentSnap.tx_bps` (TS) ✅
- `HistoryPoint.TS int64` (Go) → `ts` (JSON) → `HistoryPoint.ts: number` (TS) ✅
- `FailoverEvent.TS float64` (Go) → `ts` (JSON) → `FailoverEvent.ts: number` (TS) ✅
