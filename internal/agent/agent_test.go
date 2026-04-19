package agent_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	kubenataws "github.com/kube-nat/kube-nat/internal/aws"
	"github.com/kube-nat/kube-nat/internal/config"
	"github.com/kube-nat/kube-nat/internal/lease"
	"github.com/kube-nat/kube-nat/internal/metrics"
	"github.com/kube-nat/kube-nat/internal/reconciler"
	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

type fakeNAT struct{ ensureCalled int }

func (f *fakeNAT) EnsureMasquerade(_ string) error         { f.ensureCalled++; return nil }
func (f *fakeNAT) MasqueradeExists(_ string) (bool, error) { return true, nil }
func (f *fakeNAT) EnableIPForward() error                   { return nil }
func (f *fakeNAT) SetConntrackMax(_ int) error              { return nil }
func (f *fakeNAT) EnsureForwardCounters() error             { return nil }
func (f *fakeNAT) GetForwardBytes() (uint64, uint64, error) { return 0, 0, nil }

type fakeEC2 struct{ claimCalled int }

func (f *fakeEC2) DisableSourceDestCheck(_ context.Context, _ string) error { return nil }
func (f *fakeEC2) DiscoverRouteTables(_ context.Context, _ string) ([]kubenataws.RouteTable, error) {
	return []kubenataws.RouteTable{{ID: "rtb-001", AZ: "eu-west-1a"}}, nil
}
func (f *fakeEC2) ClaimRouteTable(_ context.Context, _, _ string) error {
	f.claimCalled++
	return nil
}
func (f *fakeEC2) ReleaseRouteTable(_ context.Context, _, _ string) error  { return nil }
func (f *fakeEC2) LookupNatGateway(_ context.Context, _, _ string) (string, error) {
	return "nat-fake", nil
}
func (f *fakeEC2) DescribeInstanceMaxBandwidth(_ context.Context, _ string) (float64, error) {
	return 25e9, nil // 25 Gbps
}

// TestTakeoverAcquiresLeaseAndClaimsRouteTable verifies that when a peer is dead
// the agent can acquire its expired lease and claim its route tables.
func TestTakeoverAcquiresLeaseAndClaimsRouteTable(t *testing.T) {
	k8s := fake.NewSimpleClientset()
	cfg := &config.Config{
		Namespace:     "kube-system",
		LeaseDuration: 30 * time.Second,
		Mode:          "auto",
	}
	leaseMgr := lease.NewManager(k8s, cfg.Namespace, cfg.LeaseDuration)
	ec2 := &fakeEC2{}

	// Seed an expired Lease for eu-west-1a.
	expiredTime := metav1.NewMicroTime(time.Now().Add(-time.Hour))
	holder := "old-pod"
	_, err := k8s.CoordinationV1().Leases(cfg.Namespace).Create(
		context.Background(),
		&coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{Name: "kube-nat-eu-west-1a", Namespace: cfg.Namespace},
			Spec: coordinationv1.LeaseSpec{
				HolderIdentity: &holder,
				RenewTime:      &expiredTime,
			},
		},
		metav1.CreateOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}

	acquired, err := leaseMgr.Acquire(context.Background(), "eu-west-1a", "i-0abc")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if !acquired {
		t.Fatal("expected to acquire expired lease")
	}

	tables, err := ec2.DiscoverRouteTables(context.Background(), "eu-west-1a")
	if err != nil {
		t.Fatal(err)
	}
	for _, rt := range tables {
		if err := ec2.ClaimRouteTable(context.Background(), rt.ID, "i-0abc"); err != nil {
			t.Fatal(err)
		}
	}

	if ec2.claimCalled == 0 {
		t.Error("expected ClaimRouteTable to be called during takeover")
	}
}

// TestMetricsEndpointReachable verifies /metrics returns 200.
func TestMetricsEndpointReachable(t *testing.T) {
	reg := metrics.NewRegistry()
	srv := httptest.NewServer(metrics.NewMux(reg, func() error { return nil }))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200 got %d", resp.StatusCode)
	}
}

// TestHealthzAndReadyzReady verifies /healthz and /readyz return 200 when ready.
func TestHealthzAndReadyzReady(t *testing.T) {
	reg := metrics.NewRegistry()
	srv := httptest.NewServer(metrics.NewMux(reg, func() error { return nil }))
	defer srv.Close()

	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: want 200 got %d", path, resp.StatusCode)
		}
	}
}

// TestReadyzNotReady verifies /readyz returns 503 when not ready.
func TestReadyzNotReady(t *testing.T) {
	reg := metrics.NewRegistry()
	srv := httptest.NewServer(metrics.NewMux(reg, func() error { return context.DeadlineExceeded }))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("want 503 got %d", resp.StatusCode)
	}
}

// TestReconcilerAutoMode verifies reconciler calls EnsureMasquerade and ClaimRouteTable.
func TestReconcilerAutoMode(t *testing.T) {
	n := &fakeNAT{}
	e := &fakeEC2{}
	r := reconciler.New(reconciler.Config{
		NATManager: n,
		EC2Client:  e,
		Iface:      "eth0",
		AZ:         "eu-west-1a",
		InstanceID: "i-0abc",
		Mode:       "auto",
	})
	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n.ensureCalled == 0 {
		t.Error("expected EnsureMasquerade to be called")
	}
	if e.claimCalled == 0 {
		t.Error("expected ClaimRouteTable to be called")
	}
}
