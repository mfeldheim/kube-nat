package reconciler_test

import (
	"context"
	"io"
	"testing"

	kubenataws "github.com/kube-nat/kube-nat/internal/aws"
	"github.com/kube-nat/kube-nat/internal/reconciler"
)

type fakeNAT struct{ ensureCalled int }

func (f *fakeNAT) EnsureMasquerade(_ string) error            { f.ensureCalled++; return nil }
func (f *fakeNAT) MasqueradeExists(_ string) (bool, error)    { return true, nil }
func (f *fakeNAT) EnableIPForward() error                     { return nil }
func (f *fakeNAT) SetConntrackMax(_ int) error                { return nil }

// fakeEC2 simulates AWS route table state. currentTarget[rtbID] tracks what
// the 0.0.0.0/0 route currently points to ("nat-xxx" or an instance ID).
type fakeEC2 struct {
	claimCalled   int
	currentTarget map[string]string // rtbID → current route target
}

func newFakeEC2(rtbID, initialTarget string) *fakeEC2 {
	return &fakeEC2{currentTarget: map[string]string{rtbID: initialTarget}}
}

func (f *fakeEC2) DisableSourceDestCheck(_ context.Context, _ string) error { return nil }

func (f *fakeEC2) DiscoverRouteTables(_ context.Context, _ string) ([]kubenataws.RouteTable, error) {
	rt := kubenataws.RouteTable{ID: "rtb-001", AZ: "eu-west-1a", VpcID: "vpc-001"}
	if target, ok := f.currentTarget["rtb-001"]; ok {
		if len(target) > 4 && target[:4] == "nat-" {
			rt.NatGatewayID = target
		} else {
			rt.InstanceID = target
		}
	}
	return []kubenataws.RouteTable{rt}, nil
}

func (f *fakeEC2) ClaimRouteTable(_ context.Context, rtbID, instanceID string) error {
	f.claimCalled++
	f.currentTarget[rtbID] = instanceID
	return nil
}

func (f *fakeEC2) ReleaseRouteTable(_ context.Context, rtbID, natGwID string) error {
	f.currentTarget[rtbID] = natGwID
	return nil
}

func (f *fakeEC2) LookupNatGateway(_ context.Context, _, _ string) (string, error) {
	return "nat-fallback", nil
}

func (f *fakeEC2) DescribeInstanceMaxBandwidth(_ context.Context, _ string) (float64, error) {
	return 25e9, nil // 25 Gbps
}

func TestReconcileVerifiesRules(t *testing.T) {
	n := &fakeNAT{}
	e := newFakeEC2("rtb-001", "nat-0abc") // starts pointing at NAT GW
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
		t.Error("expected ClaimRouteTable to be called when route points at NAT GW")
	}
}

func TestManualModeSkipsRouteClaim(t *testing.T) {
	n := &fakeNAT{}
	e := newFakeEC2("rtb-001", "nat-0abc")
	r := reconciler.New(reconciler.Config{
		NATManager: n,
		EC2Client:  e,
		Iface:      "eth0",
		AZ:         "eu-west-1a",
		InstanceID: "i-0abc",
		Mode:       "manual",
		LogWriter:  io.Discard,
	})
	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.claimCalled != 0 {
		t.Error("expected ClaimRouteTable NOT to be called in manual mode")
	}
}

func TestReconcileIdempotent(t *testing.T) {
	n := &fakeNAT{}
	e := newFakeEC2("rtb-001", "nat-0abc") // starts pointing at NAT GW
	r := reconciler.New(reconciler.Config{
		NATManager: n,
		EC2Client:  e,
		Iface:      "eth0",
		AZ:         "eu-west-1a",
		InstanceID: "i-0abc",
		Mode:       "auto",
	})
	for i := 0; i < 3; i++ {
		if err := r.Reconcile(context.Background()); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if n.ensureCalled != 3 {
		t.Errorf("want 3 EnsureMasquerade calls got %d", n.ensureCalled)
	}
	// First reconcile claims (nat GW → instance). Subsequent ticks see the
	// route already points at us and skip ReplaceRoute.
	if e.claimCalled != 1 {
		t.Errorf("want 1 ClaimRouteTable call (only on first tick, then idempotent) got %d", e.claimCalled)
	}
}

func TestReconcileRecorrectsRouteIfTampered(t *testing.T) {
	n := &fakeNAT{}
	e := newFakeEC2("rtb-001", "nat-0abc")
	r := reconciler.New(reconciler.Config{
		NATManager: n,
		EC2Client:  e,
		Iface:      "eth0",
		AZ:         "eu-west-1a",
		InstanceID: "i-0abc",
		Mode:       "auto",
		LogWriter:  io.Discard,
	})

	// First reconcile: claims the route.
	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.claimCalled != 1 {
		t.Fatalf("want 1 claim after first tick, got %d", e.claimCalled)
	}

	// Simulate external tampering: route changed back to a different instance.
	e.currentTarget["rtb-001"] = "i-intruder"

	// Next reconcile should detect and re-claim.
	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.claimCalled != 2 {
		t.Errorf("want 2 claims after tampering, got %d", e.claimCalled)
	}
	if e.currentTarget["rtb-001"] != "i-0abc" {
		t.Errorf("want route restored to i-0abc, got %s", e.currentTarget["rtb-001"])
	}
}

