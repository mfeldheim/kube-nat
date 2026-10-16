package agent

import (
	"context"
	"log"
	"os"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// disruptionTaint is the taint node provisioners (e.g. Karpenter) apply to a
// node shortly before terminating it. It appears before the node is gone, so it
// gives the agent a chance to hand the AZ over cleanly.
const disruptionTaint = "karpenter.sh/disrupted"

// DrainMonitor watches the node this agent runs on and reports whether the node
// is draining: cordoned (spec.unschedulable) or marked for disruption.
//
// A DaemonSet is always scheduled onto cordoned nodes — the DaemonSet controller
// force-adds a node.kubernetes.io/unschedulable toleration — so "do not run an
// agent on a draining node" cannot be expressed as a scheduling rule. Instead
// the agent stands down on its own: while its node is draining it releases its
// AZ Lease and stops claiming the AZ's route table, so the AZ's replacement
// agent (or a peer) takes over instead of the two fighting over the route.
type DrainMonitor struct {
	client    kubernetes.Interface
	namespace string
	podName   string
	nodeName  string
	interval  time.Duration
	logger    *log.Logger
	onDrain   func()

	draining atomic.Bool
}

// NewDrainMonitor returns a monitor for this agent's node. onDrain is called once
// on each transition into the draining state (with the draining flag already
// set) so the caller can hand the AZ over.
func NewDrainMonitor(client kubernetes.Interface, namespace, podName string, interval time.Duration, logger *log.Logger, onDrain func()) *DrainMonitor {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &DrainMonitor{
		client:    client,
		namespace: namespace,
		podName:   podName,
		interval:  interval,
		logger:    logger,
		onDrain:   onDrain,
	}
}

// IsDraining reports whether the node was draining at the last check.
func (m *DrainMonitor) IsDraining() bool { return m.draining.Load() }

// NodeName returns the resolved node name, or "" if it could not be determined.
func (m *DrainMonitor) NodeName() string { return m.nodeName }

// Prime resolves the node name and performs a first check synchronously, so the
// startup path sees an already-draining node. Returns false if the node could
// not be resolved (in which case the monitor will keep serving).
func (m *DrainMonitor) Prime(ctx context.Context) bool {
	if !m.resolveNode(ctx) {
		return false
	}
	m.check(ctx)
	return true
}

// Run polls the node's drain state until ctx is done.
func (m *DrainMonitor) Run(ctx context.Context) {
	if m.nodeName == "" && !m.resolveNode(ctx) {
		// Cannot tell whether we are draining — keep serving rather than risk
		// taking a healthy AZ out of service.
		return
	}
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.check(ctx)
		}
	}
}

func (m *DrainMonitor) check(ctx context.Context) {
	node, err := m.client.CoreV1().Nodes().Get(ctx, m.nodeName, metav1.GetOptions{})
	if err != nil {
		// Fail open: a transient API error must not take the AZ out of service.
		m.logger.Printf("drain check: get node %s: %v", m.nodeName, err)
		return
	}

	draining := NodeIsDraining(node)
	if draining == m.draining.Load() {
		return
	}
	if draining {
		m.logger.Printf("node %s is draining (cordoned or disrupted) — standing down: releasing AZ lease and stopping route claims", m.nodeName)
		m.draining.Store(true)
		if m.onDrain != nil {
			m.onDrain()
		}
		return
	}
	m.logger.Printf("node %s is no longer draining — resuming service", m.nodeName)
	m.draining.Store(false)
}

// NodeIsDraining reports whether a node is being drained: it is cordoned or has
// been marked for disruption by the node provisioner.
func NodeIsDraining(node *corev1.Node) bool {
	if node.Spec.Unschedulable {
		return true
	}
	for _, t := range node.Spec.Taints {
		if t.Key == disruptionTaint {
			return true
		}
	}
	return false
}

// resolveNode determines the node name from the NODE_NAME env var (injected via
// the downward API) or, failing that, by finding this pod.
func (m *DrainMonitor) resolveNode(ctx context.Context) bool {
	if m.nodeName != "" {
		return true
	}
	if n := os.Getenv("NODE_NAME"); n != "" {
		m.nodeName = n
		return true
	}
	// RBAC grants pods list (not get), so look ourselves up through a list.
	pods, err := m.client.CoreV1().Pods(m.namespace).List(ctx, metav1.ListOptions{
		FieldSelector: "metadata.name=" + m.podName,
	})
	if err != nil || len(pods.Items) == 0 || pods.Items[0].Spec.NodeName == "" {
		m.logger.Printf("drain stand-down disabled: could not determine node name for pod %s (set NODE_NAME via the downward API): %v", m.podName, err)
		return false
	}
	m.nodeName = pods.Items[0].Spec.NodeName
	return true
}
