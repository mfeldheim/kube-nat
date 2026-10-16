package lease

import (
	"context"
	"fmt"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// homeAZLabel records the AZ that the holder agent actually runs in. A Lease for
// AZ X may be held either by X's own agent (label == X) or by a failover agent
// from another AZ (label != X). This is what lets the returning own agent
// reclaim its Lease from a failover holder without a blind steal.
const homeAZLabel = "kube-nat/home-az"

type Manager struct {
	client        kubernetes.Interface
	namespace     string
	leaseDuration time.Duration
	homeAZ        string
}

// NewManager returns a Lease manager. homeAZ is the AZ this agent runs in; it is
// stored on every Lease this agent writes and drives the own-AZ preemption rule.
func NewManager(client kubernetes.Interface, namespace string, duration time.Duration, homeAZ string) *Manager {
	return &Manager{
		client:        client,
		namespace:     namespace,
		leaseDuration: duration,
		homeAZ:        homeAZ,
	}
}

// Renew creates the Lease for az if it does not exist, or extends it if this
// agent already holds it. It never steals a Lease from another live holder.
// Returns true when the Lease is (now) held by holderID.
func (m *Manager) Renew(ctx context.Context, az, holderID string) (bool, error) {
	name := leaseName(az)
	now := metav1.NewMicroTime(time.Now())
	durSecs := int32(m.leaseDuration.Seconds())

	existing, err := m.client.CoordinationV1().Leases(m.namespace).
		Get(ctx, name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		if _, err := m.create(ctx, name, holderID, now, durSecs); err != nil {
			if errors.IsAlreadyExists(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("get lease %s: %w", name, err)
	}
	if existing.Spec.HolderIdentity == nil || *existing.Spec.HolderIdentity != holderID {
		// Held by someone else — never steal on a renew. Ownership changes only
		// through Acquire, which is gated on expiry or own-AZ preemption.
		return false, nil
	}

	existing.Spec.RenewTime = &now
	existing.Spec.LeaseDurationSeconds = &durSecs
	setHomeAZ(existing, m.homeAZ)
	if _, err := m.client.CoordinationV1().Leases(m.namespace).
		Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		if errors.IsConflict(err) {
			// Someone else updated the Lease first — clean loss.
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Acquire takes the Lease for az when it is absent, expired, or currently held
// by a failover agent from a different AZ while this agent runs in az (its own
// AZ). Returns true when this agent now holds the Lease.
func (m *Manager) Acquire(ctx context.Context, az, holderID string) (bool, error) {
	name := leaseName(az)
	now := metav1.NewMicroTime(time.Now())
	durSecs := int32(m.leaseDuration.Seconds())

	existing, err := m.client.CoordinationV1().Leases(m.namespace).
		Get(ctx, name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		if _, err := m.create(ctx, name, holderID, now, durSecs); err != nil {
			if errors.IsAlreadyExists(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}
	if err != nil {
		return false, err
	}

	if existing.Spec.HolderIdentity != nil && *existing.Spec.HolderIdentity == holderID {
		// Already ours — refresh the TTL.
		return m.write(ctx, existing, holderID, now, durSecs)
	}

	expired := IsExpired(existing, time.Now())
	// Preemption: this agent runs in az (the Lease's own AZ) while the current
	// holder is a failover agent from a different AZ. The genuine owner always
	// wins over a stand-in.
	heldByForeignFailover := m.homeAZ == az && holderHomeAZ(existing) != "" && holderHomeAZ(existing) != az
	if !expired && !heldByForeignFailover {
		return false, nil
	}

	return m.write(ctx, existing, holderID, now, durSecs)
}

// Release deletes the Lease for az if it is currently held by holderID. Used on
// graceful shutdown so a failover agent can acquire the Lease immediately
// instead of waiting for it to expire.
func (m *Manager) Release(ctx context.Context, az, holderID string) error {
	name := leaseName(az)
	existing, err := m.client.CoordinationV1().Leases(m.namespace).
		Get(ctx, name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if existing.Spec.HolderIdentity == nil || *existing.Spec.HolderIdentity != holderID {
		return nil
	}
	rv := existing.ResourceVersion
	err = m.client.CoordinationV1().Leases(m.namespace).
		Delete(ctx, name, metav1.DeleteOptions{
			Preconditions: &metav1.Preconditions{ResourceVersion: &rv},
		})
	if err != nil && (errors.IsNotFound(err) || errors.IsConflict(err)) {
		return nil
	}
	return err
}

// write performs a compare-and-set update of the Lease holder. A conflict means
// another agent won the race — reported as a clean loss (false, nil).
func (m *Manager) write(ctx context.Context, l *coordinationv1.Lease, holderID string, now metav1.MicroTime, durSecs int32) (bool, error) {
	l.Spec.HolderIdentity = &holderID
	l.Spec.RenewTime = &now
	l.Spec.LeaseDurationSeconds = &durSecs
	setHomeAZ(l, m.homeAZ)
	if _, err := m.client.CoordinationV1().Leases(m.namespace).
		Update(ctx, l, metav1.UpdateOptions{}); err != nil {
		if errors.IsConflict(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (m *Manager) create(ctx context.Context, name, holderID string, now metav1.MicroTime, durSecs int32) (*coordinationv1.Lease, error) {
	labels := map[string]string{"app": "kube-nat"}
	if m.homeAZ != "" {
		labels[homeAZLabel] = m.homeAZ
	}
	return m.client.CoordinationV1().Leases(m.namespace).Create(ctx,
		&coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: m.namespace,
				Labels:    labels,
			},
			Spec: coordinationv1.LeaseSpec{
				HolderIdentity:       &holderID,
				LeaseDurationSeconds: &durSecs,
				RenewTime:            &now,
			},
		}, metav1.CreateOptions{})
}

func setHomeAZ(l *coordinationv1.Lease, az string) {
	if az == "" {
		return
	}
	if l.Labels == nil {
		l.Labels = map[string]string{}
	}
	l.Labels[homeAZLabel] = az
}

func holderHomeAZ(l *coordinationv1.Lease) string {
	if l.Labels == nil {
		return ""
	}
	return l.Labels[homeAZLabel]
}

// IsExpired returns true if the lease's renew time + duration is in the past.
func IsExpired(l *coordinationv1.Lease, now time.Time) bool {
	if l.Spec.RenewTime == nil || l.Spec.LeaseDurationSeconds == nil {
		return true
	}
	expiry := l.Spec.RenewTime.Add(time.Duration(*l.Spec.LeaseDurationSeconds) * time.Second)
	return now.After(expiry)
}

// ListExpiredAZs returns AZs whose Leases are expired, excluding ownAZ.
func (m *Manager) ListExpiredAZs(ctx context.Context, ownAZ string) ([]string, error) {
	list, err := m.client.CoordinationV1().Leases(m.namespace).
		List(ctx, metav1.ListOptions{LabelSelector: "app=kube-nat"})
	if err != nil {
		return nil, err
	}
	var expired []string
	now := time.Now()
	for _, l := range list.Items {
		az := azFromLeaseName(l.Name)
		if az == "" || az == ownAZ {
			continue
		}
		if IsExpired(&l, now) {
			expired = append(expired, az)
		}
	}
	return expired, nil
}

func leaseName(az string) string { return "kube-nat-" + az }

func azFromLeaseName(name string) string {
	const prefix = "kube-nat-"
	if len(name) > len(prefix) {
		return name[len(prefix):]
	}
	return ""
}
