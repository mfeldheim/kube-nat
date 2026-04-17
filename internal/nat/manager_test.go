package nat_test

import (
	"testing"

	"github.com/kube-nat/kube-nat/internal/nat"
)

type fakeNAT struct {
	rules   map[string]bool
	forward bool
	connmax int
}

func (f *fakeNAT) EnsureMasquerade(_ string) error {
	if f.rules == nil {
		f.rules = make(map[string]bool)
	}
	f.rules["MASQUERADE"] = true
	return nil
}

func (f *fakeNAT) MasqueradeExists(_ string) (bool, error) {
	return f.rules["MASQUERADE"], nil
}

func (f *fakeNAT) EnableIPForward() error {
	f.forward = true
	return nil
}

func (f *fakeNAT) SetConntrackMax(max int) error {
	f.connmax = max
	return nil
}

func TestNATManagerInterface(t *testing.T) {
	var _ nat.Manager = &fakeNAT{}
}

func TestEnsureMasqueradeIdempotent(t *testing.T) {
	f := &fakeNAT{}
	if err := f.EnsureMasquerade("eth0"); err != nil {
		t.Fatal(err)
	}
	if err := f.EnsureMasquerade("eth0"); err != nil {
		t.Fatalf("second call should be idempotent: %v", err)
	}
	exists, _ := f.MasqueradeExists("eth0")
	if !exists {
		t.Error("rule should exist after EnsureMasquerade")
	}
}

func TestEnableIPForward(t *testing.T) {
	f := &fakeNAT{}
	if err := f.EnableIPForward(); err != nil {
		t.Fatal(err)
	}
	if !f.forward {
		t.Error("expected forward=true after EnableIPForward")
	}
}

func TestSetConntrackMax(t *testing.T) {
	f := &fakeNAT{}
	if err := f.SetConntrackMax(131072); err != nil {
		t.Fatal(err)
	}
	if f.connmax != 131072 {
		t.Errorf("want 131072 got %d", f.connmax)
	}
}
