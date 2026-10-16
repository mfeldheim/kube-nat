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

const ns = "kube-system"

// seedLease creates a pre-existing Lease. homeAZ sets the kube-nat/home-az label
// (empty = label absent, as written by agents before this feature existed).
func seedLease(t *testing.T, client *fake.Clientset, name, holder, homeAZ string, renewAgo, dur time.Duration) *coordinationv1.Lease {
	t.Helper()
	holderCopy := holder
	renew := metav1.NewMicroTime(time.Now().Add(-renewAgo))
	secs := int32(dur.Seconds())
	obj := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    map[string]string{"app": "kube-nat"},
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       &holderCopy,
			LeaseDurationSeconds: &secs,
			RenewTime:            &renew,
		},
	}
	if homeAZ != "" {
		obj.Labels["kube-nat/home-az"] = homeAZ
	}
	created, err := client.CoordinationV1().Leases(ns).Create(context.Background(), obj, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	return created
}

func getLease(t *testing.T, client *fake.Clientset, name string) *coordinationv1.Lease {
	t.Helper()
	l, err := client.CoordinationV1().Leases(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get lease %s: %v", name, err)
	}
	return l
}

func TestRenewCreatesLease(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := lease.NewManager(client, ns, 30*time.Second, "eu-west-1a")

	renewed, err := mgr.Renew(context.Background(), "eu-west-1a", "kube-nat-abc")
	if err != nil {
		t.Fatal(err)
	}
	if !renewed {
		t.Fatal("want renewed=true on fresh create")
	}

	l := getLease(t, client, "kube-nat-eu-west-1a")
	if *l.Spec.HolderIdentity != "kube-nat-abc" {
		t.Errorf("want holder kube-nat-abc got %s", *l.Spec.HolderIdentity)
	}
	if l.Labels["kube-nat/home-az"] != "eu-west-1a" {
		t.Errorf("want home-az label eu-west-1a got %q", l.Labels["kube-nat/home-az"])
	}
}

func TestRenewUpdatesExisting(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := lease.NewManager(client, ns, 30*time.Second, "eu-west-1a")

	if ok, err := mgr.Renew(context.Background(), "eu-west-1a", "pod-1"); err != nil || !ok {
		t.Fatalf("first renew: ok=%v err=%v", ok, err)
	}
	if ok, err := mgr.Renew(context.Background(), "eu-west-1a", "pod-1"); err != nil || !ok {
		t.Fatalf("second renew should succeed: ok=%v err=%v", ok, err)
	}
}

// A live Lease held by a different pod must never be stolen by Renew.
func TestRenewDoesNotSteal(t *testing.T) {
	client := fake.NewSimpleClientset()
	seedLease(t, client, "kube-nat-eu-west-1a", "pod-1", "eu-west-1a", time.Second, 30*time.Second)
	mgr := lease.NewManager(client, ns, 30*time.Second, "eu-west-1a")

	ok, err := mgr.Renew(context.Background(), "eu-west-1a", "pod-2")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("Renew must not steal a live lease held by another pod")
	}
	if got := *getLease(t, client, "kube-nat-eu-west-1a").Spec.HolderIdentity; got != "pod-1" {
		t.Errorf("holder changed to %q, want pod-1", got)
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
	seedLease(t, client, "kube-nat-eu-west-1b", "old-pod", "eu-west-1b", 60*time.Second, 15*time.Second)
	mgr := lease.NewManager(client, ns, 30*time.Second, "eu-west-1a")

	acquired, err := mgr.Acquire(context.Background(), "eu-west-1b", "new-pod")
	if err != nil {
		t.Fatal(err)
	}
	if !acquired {
		t.Error("want acquired=true for expired lease")
	}
	if got := *getLease(t, client, "kube-nat-eu-west-1b").Spec.HolderIdentity; got != "new-pod" {
		t.Errorf("want holder new-pod got %s", got)
	}
}

// A foreign agent (home AZ != lease AZ) must not acquire a live Lease.
func TestAcquireDoesNotStealLiveForeignLease(t *testing.T) {
	client := fake.NewSimpleClientset()
	seedLease(t, client, "kube-nat-eu-west-1b", "pod-1b", "eu-west-1b", time.Second, 30*time.Second)
	mgr := lease.NewManager(client, ns, 30*time.Second, "eu-west-1a") // agent in 1a

	acquired, err := mgr.Acquire(context.Background(), "eu-west-1b", "pod-1a")
	if err != nil {
		t.Fatal(err)
	}
	if acquired {
		t.Error("a foreign agent must not acquire a live lease")
	}
}

// The AZ's own agent preempts a live Lease currently held by a foreign failover agent.
func TestAcquirePreemptsForeignFailover(t *testing.T) {
	client := fake.NewSimpleClientset()
	// eu-west-1a's lease is held by the 1b agent (failover), still live.
	seedLease(t, client, "kube-nat-eu-west-1a", "pod-1b", "eu-west-1b", time.Second, 30*time.Second)
	mgr := lease.NewManager(client, ns, 30*time.Second, "eu-west-1a") // agent in 1a

	acquired, err := mgr.Acquire(context.Background(), "eu-west-1a", "pod-1a")
	if err != nil {
		t.Fatal(err)
	}
	if !acquired {
		t.Error("own-AZ agent should preempt a foreign failover holder")
	}
	if got := *getLease(t, client, "kube-nat-eu-west-1a").Spec.HolderIdentity; got != "pod-1a" {
		t.Errorf("want holder pod-1a got %s", got)
	}
}

// The own agent must not preempt a live Lease held by another own-AZ agent
// (e.g. a rolling restart while the old pod has not yet released).
func TestAcquireDoesNotPreemptOwnAZHolder(t *testing.T) {
	client := fake.NewSimpleClientset()
	seedLease(t, client, "kube-nat-eu-west-1a", "old-1a", "eu-west-1a", time.Second, 30*time.Second)
	mgr := lease.NewManager(client, ns, 30*time.Second, "eu-west-1a")

	acquired, err := mgr.Acquire(context.Background(), "eu-west-1a", "new-1a")
	if err != nil {
		t.Fatal(err)
	}
	if acquired {
		t.Error("own-AZ agent must not preempt another own-AZ holder before expiry")
	}
}

func TestAcquireIsIdempotentWhenHeld(t *testing.T) {
	client := fake.NewSimpleClientset()
	seedLease(t, client, "kube-nat-eu-west-1a", "pod-1", "eu-west-1a", time.Second, 30*time.Second)
	mgr := lease.NewManager(client, ns, 30*time.Second, "eu-west-1a")

	acquired, err := mgr.Acquire(context.Background(), "eu-west-1a", "pod-1")
	if err != nil || !acquired {
		t.Fatalf("re-acquiring our own live lease should succeed: acquired=%v err=%v", acquired, err)
	}
}

func TestReleaseDeletesOwnLeaseOnly(t *testing.T) {
	client := fake.NewSimpleClientset()
	seedLease(t, client, "kube-nat-eu-west-1a", "pod-1", "eu-west-1a", time.Second, 30*time.Second)
	mgr := lease.NewManager(client, ns, 30*time.Second, "eu-west-1a")

	// A non-holder must not delete it.
	if err := mgr.Release(context.Background(), "eu-west-1a", "pod-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoordinationV1().Leases(ns).Get(context.Background(), "kube-nat-eu-west-1a", metav1.GetOptions{}); err != nil {
		t.Fatalf("lease should still exist after non-holder release: %v", err)
	}

	// The holder deletes it.
	if err := mgr.Release(context.Background(), "eu-west-1a", "pod-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoordinationV1().Leases(ns).Get(context.Background(), "kube-nat-eu-west-1a", metav1.GetOptions{}); err == nil {
		t.Error("lease should be deleted after holder release")
	}
}

func TestListExpiredAZs(t *testing.T) {
	client := fake.NewSimpleClientset()
	seedLease(t, client, "kube-nat-eu-west-1a", "pod-1a", "eu-west-1a", time.Second, 30*time.Second)     // live
	seedLease(t, client, "kube-nat-eu-west-1b", "pod-1b", "eu-west-1b", 120*time.Second, 30*time.Second) // expired
	seedLease(t, client, "kube-nat-eu-west-1c", "pod-1c", "eu-west-1c", 120*time.Second, 30*time.Second) // expired

	mgr := lease.NewManager(client, ns, 30*time.Second, "eu-west-1a")
	expired, err := mgr.ListExpiredAZs(context.Background(), "eu-west-1a")
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 2 {
		t.Fatalf("want 2 expired AZs got %v", expired)
	}
	for _, az := range expired {
		if az == "eu-west-1a" {
			t.Error("own AZ must be excluded")
		}
	}
}

func int32Ptr(i int32) *int32 { return &i }
