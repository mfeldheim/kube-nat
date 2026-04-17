package collector_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/kube-nat/kube-nat/internal/collector"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

const metricsFixture = `# HELP kube_nat_bytes_tx_total Total bytes transmitted
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

func TestCollectBuildsAgentSnap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.Write([]byte(metricsFixture))
	}))
	defer srv.Close()

	_, portStr, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)

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
		MetricsPort:    port,
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
		t.Errorf("expected [rtb-001], got %v", a.RouteTablesOwned)
	}
	if a.InstanceID != "i-0abc" {
		t.Errorf("want instance_id=i-0abc got %q", a.InstanceID)
	}
}

func TestCollectRateZeroOnFirstScrape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.Write([]byte(metricsFixture))
	}))
	defer srv.Close()

	_, portStr, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)

	k8s := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "kube-nat-agent-abc", Namespace: "kube-system",
			Labels: map[string]string{"app": "kube-nat", "component": "agent"},
		},
		Status: corev1.PodStatus{PodIP: "127.0.0.1"},
	})

	c := collector.New(collector.Config{K8sClient: k8s, Namespace: "kube-system", MetricsPort: port, ScrapeInterval: 5 * time.Second})
	snap, err := c.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// First scrape has no previous sample — rates must be 0
	if snap.Agents[0].TxBytesPerSec != 0 {
		t.Errorf("expected tx_bps=0 on first scrape, got %v", snap.Agents[0].TxBytesPerSec)
	}
}
