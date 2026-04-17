package lease_test

import (
	"context"
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/kube-nat/kube-nat/internal/lease"
)

func TestRenewCreatesLease(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := lease.NewManager(client, "kube-system", 15*time.Second)

	if err := mgr.Renew(context.Background(), "eu-west-1a", "kube-nat-abc"); err != nil {
		t.Fatal(err)
	}

	l, err := client.CoordinationV1().Leases("kube-system").
		Get(context.Background(), "kube-nat-eu-west-1a", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if *l.Spec.HolderIdentity != "kube-nat-abc" {
		t.Errorf("want holder kube-nat-abc got %s", *l.Spec.HolderIdentity)
	}
}

func TestRenewUpdatesExisting(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := lease.NewManager(client, "kube-system", 15*time.Second)

	if err := mgr.Renew(context.Background(), "eu-west-1a", "pod-1"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Renew(context.Background(), "eu-west-1a", "pod-1"); err != nil {
		t.Fatalf("second renew should not error: %v", err)
	}
}

func TestIsExpired(t *testing.T) {
	now := time.Now()
	renewTime := metav1.NewMicroTime(now.Add(-20 * time.Second))
	l := &coordinationv1.Lease{
		Spec: coordinationv1.LeaseSpec{
			RenewTime:            &renewTime,
			LeaseDurationSeconds: int32Ptr(15),
		},
	}
	if !lease.IsExpired(l, now) {
		t.Error("lease renewed 20s ago with 15s duration should be expired")
	}
}

func TestIsNotExpired(t *testing.T) {
	now := time.Now()
	renewTime := metav1.NewMicroTime(now.Add(-5 * time.Second))
	l := &coordinationv1.Lease{
		Spec: coordinationv1.LeaseSpec{
			RenewTime:            &renewTime,
			LeaseDurationSeconds: int32Ptr(15),
		},
	}
	if lease.IsExpired(l, now) {
		t.Error("lease renewed 5s ago with 15s duration should not be expired")
	}
}

func TestAcquireExpiredLease(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := lease.NewManager(client, "kube-system", 15*time.Second)

	// Create expired lease
	old := metav1.NewMicroTime(time.Now().Add(-60 * time.Second))
	dur := int32(15)
	holder := "old-pod"
	client.CoordinationV1().Leases("kube-system").Create(context.Background(),
		&coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{Name: "kube-nat-eu-west-1b", Namespace: "kube-system"},
			Spec: coordinationv1.LeaseSpec{
				HolderIdentity:       &holder,
				LeaseDurationSeconds: &dur,
				RenewTime:            &old,
			},
		}, metav1.CreateOptions{})

	acquired, err := mgr.Acquire(context.Background(), "eu-west-1b", "new-pod")
	if err != nil {
		t.Fatal(err)
	}
	if !acquired {
		t.Error("want acquired=true for expired lease")
	}
}

func int32Ptr(i int32) *int32 { return &i }
