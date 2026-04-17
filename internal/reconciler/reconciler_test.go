package reconciler_test

import (
	"context"
	"io"
	"testing"

	kubenataws "github.com/kube-nat/kube-nat/internal/aws"
	"github.com/kube-nat/kube-nat/internal/reconciler"
)

type fakeNAT struct{ ensureCalled int }

func (f *fakeNAT) EnsureMasquerade(_ string) error  { f.ensureCalled++; return nil }
func (f *fakeNAT) MasqueradeExists(_ string) (bool, error) { return true, nil }
func (f *fakeNAT) EnableIPForward() error            { return nil }
func (f *fakeNAT) SetConntrackMax(_ int) error       { return nil }

type fakeEC2 struct{ claimCalled int }

func (f *fakeEC2) DisableSourceDestCheck(_ context.Context, _ string) error { return nil }
func (f *fakeEC2) DiscoverRouteTables(_ context.Context, _ string) ([]kubenataws.RouteTable, error) {
	return []kubenataws.RouteTable{{ID: "rtb-001", AZ: "eu-west-1a"}}, nil
}
func (f *fakeEC2) ClaimRouteTable(_ context.Context, _, _ string) error {
	f.claimCalled++
	return nil
}

func TestReconcileVerifiesRules(t *testing.T) {
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
		t.Error("expected ClaimRouteTable to be called in auto mode")
	}
}

func TestManualModeSkipsRouteClaim(t *testing.T) {
	n := &fakeNAT{}
	e := &fakeEC2{}
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
	e := &fakeEC2{}
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
}
