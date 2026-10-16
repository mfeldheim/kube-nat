package agent

import (
	"context"
	"io"
	"log"
	"testing"
	"time"

	kubenataws "github.com/kube-nat/kube-nat/internal/aws"
	"github.com/kube-nat/kube-nat/internal/lease"
	"github.com/kube-nat/kube-nat/internal/reconciler"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

type noopNAT struct{}

func (noopNAT) EnsureMasquerade(string) error         { return nil }
func (noopNAT) MasqueradeExists(string) (bool, error) { return true, nil }
func (noopNAT) EnableIPForward() error                { return nil }
func (noopNAT) SetConntrackMax(int) error             { return nil }

// countingEC2 reports a route table that points somewhere else, so every
// successful serve attempt has to issue a claim.
type countingEC2 struct{ claims int }

func (c *countingEC2) DisableSourceDestCheck(context.Context, string) error { return nil }
func (c *countingEC2) DiscoverRouteTables(_ context.Context, az string) ([]kubenataws.RouteTable, error) {
	return []kubenataws.RouteTable{{ID: "rtb-" + az, AZ: az, InstanceID: "i-someone-else"}}, nil
}
func (c *countingEC2) ClaimRouteTable(context.Context, string, string) error {
	c.claims++
	return nil
}
func (c *countingEC2) ReleaseRouteTable(context.Context, string, string) error { return nil }
func (c *countingEC2) LookupNatGateway(context.Context, string, string) (string, error) {
	return "nat-1", nil
}
func (c *countingEC2) DescribeInstanceMaxBandwidth(context.Context, string) (float64, error) {
	return 1e9, nil
}

func newTestReconciler(az, instanceID string, ec2 kubenataws.EC2Client) *reconciler.Reconciler {
	return reconciler.New(reconciler.Config{
		NATManager: noopNAT{},
		EC2Client:  ec2,
		Iface:      "eth0",
		AZ:         az,
		InstanceID: instanceID,
		Mode:       "auto",
	})
}

// TestTwoAgentsInSameAZDoNotBothClaim is the regression test for the route-table
// flap seen when a NAT node is replaced: for a while the old node and its
// replacement both run an agent in the same AZ. Only the agent holding the AZ's
// Lease may claim the route table, so the two can never point it at themselves in
// turns.
func TestTwoAgentsInSameAZDoNotBothClaim(t *testing.T) {
	ctx := context.Background()
	logger := log.New(io.Discard, "", 0)
	k8s := fake.NewSimpleClientset()
	const az = "eu-west-1c"

	mgrA := lease.NewManager(k8s, "kube-nat", 30*time.Second, az)
	mgrB := lease.NewManager(k8s, "kube-nat", 30*time.Second, az)
	ec2A, ec2B := &countingEC2{}, &countingEC2{}
	recA := newTestReconciler(az, "i-nodeA", ec2A)
	recB := newTestReconciler(az, "i-nodeB", ec2B)

	// The old node's agent gets there first and serves the AZ.
	servingA, err := serveOwnAZ(ctx, mgrA, recA, az, "agent-a", logger)
	if err != nil || !servingA {
		t.Fatalf("agent A should serve the AZ: serving=%v err=%v", servingA, err)
	}
	if ec2A.claims != 1 {
		t.Fatalf("agent A should have claimed the route table once, got %d claims", ec2A.claims)
	}

	// The replacement node's agent starts in the same AZ. It must stand by.
	for i := 0; i < 3; i++ {
		servingB, err := serveOwnAZ(ctx, mgrB, recB, az, "agent-b", logger)
		if err != nil {
			t.Fatalf("agent B serve attempt %d: %v", i, err)
		}
		if servingB {
			t.Fatal("agent B must stand by while agent A holds the AZ lease")
		}
	}
	if ec2B.claims != 0 {
		t.Fatalf("agent B must not claim while standing by, got %d claims", ec2B.claims)
	}

	// The old node's agent steps down (drain/terminate); the replacement takes over.
	if err := mgrA.Release(ctx, az, "agent-a"); err != nil {
		t.Fatalf("release: %v", err)
	}
	servingB, err := serveOwnAZ(ctx, mgrB, recB, az, "agent-b", logger)
	if err != nil || !servingB {
		t.Fatalf("agent B should take over after A released: serving=%v err=%v", servingB, err)
	}
	if ec2B.claims != 1 {
		t.Fatalf("agent B should claim once after taking over, got %d claims", ec2B.claims)
	}
}

// TestServeOwnAZPreemptsForeignFailover verifies the returning own-AZ agent still
// reclaims the AZ from a failover holder in another AZ.
func TestServeOwnAZPreemptsForeignFailover(t *testing.T) {
	ctx := context.Background()
	logger := log.New(io.Discard, "", 0)
	k8s := fake.NewSimpleClientset()
	const az = "eu-west-1c"

	// A failover agent from eu-west-1a currently holds eu-west-1c's lease and route.
	mgrForeign := lease.NewManager(k8s, "kube-nat", 30*time.Second, "eu-west-1a")
	ec2Foreign := &countingEC2{}
	recForeign := newTestReconciler(az, "i-nodeA", ec2Foreign)
	if serving, err := serveOwnAZ(ctx, mgrForeign, recForeign, az, "agent-a", logger); err != nil || !serving {
		t.Fatalf("foreign failover should serve: serving=%v err=%v", serving, err)
	}

	// eu-west-1c's own agent returns and must reclaim its AZ.
	mgrOwn := lease.NewManager(k8s, "kube-nat", 30*time.Second, az)
	ec2Own := &countingEC2{}
	recOwn := newTestReconciler(az, "i-nodeB", ec2Own)
	serving, err := serveOwnAZ(ctx, mgrOwn, recOwn, az, "agent-b", logger)
	if err != nil || !serving {
		t.Fatalf("own-AZ agent should preempt the foreign failover holder: serving=%v err=%v", serving, err)
	}
	if ec2Own.claims != 1 {
		t.Fatalf("own-AZ agent should claim the route table, got %d claims", ec2Own.claims)
	}
}

func TestNodeIsDraining(t *testing.T) {
	plain := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n"}}
	if NodeIsDraining(plain) {
		t.Error("a plain node is not draining")
	}

	cordoned := plain.DeepCopy()
	cordoned.Spec.Unschedulable = true
	if !NodeIsDraining(cordoned) {
		t.Error("a cordoned node is draining")
	}

	disrupted := plain.DeepCopy()
	disrupted.Spec.Taints = []corev1.Taint{{Key: disruptionTaint, Effect: corev1.TaintEffectNoSchedule}}
	if !NodeIsDraining(disrupted) {
		t.Error("a node marked for disruption is draining")
	}
}

// TestDrainMonitorStandDown verifies the agent notices its node draining,
// fires the hand-over callback exactly once, and resumes when it is uncordoned.
func TestDrainMonitorStandDown(t *testing.T) {
	ctx := context.Background()
	logger := log.New(io.Discard, "", 0)

	k8s := fake.NewSimpleClientset(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
		Spec:       corev1.NodeSpec{Unschedulable: true},
	})
	t.Setenv("NODE_NAME", "node-1")

	var calls int
	mon := NewDrainMonitor(k8s, "kube-nat", "agent-a", time.Hour, logger, func() { calls++ })
	if !mon.Prime(ctx) {
		t.Fatal("Prime should resolve the node")
	}
	if !mon.IsDraining() {
		t.Fatal("a cordoned node should report draining")
	}
	if calls != 1 {
		t.Fatalf("onDrain should fire once, got %d", calls)
	}

	// Another poll must not re-fire while still draining.
	mon.check(ctx)
	if calls != 1 {
		t.Fatalf("onDrain must fire only on the transition, got %d", calls)
	}

	// Uncordoning resumes service without firing onDrain again.
	node, err := k8s.CoreV1().Nodes().Get(ctx, "node-1", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	node.Spec.Unschedulable = false
	if _, err := k8s.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	mon.check(ctx)
	if mon.IsDraining() {
		t.Error("an uncordoned node should not report draining")
	}
	if calls != 1 {
		t.Errorf("onDrain must not fire on resume, got %d calls", calls)
	}
}
