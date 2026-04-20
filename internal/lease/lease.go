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

type Manager struct {
	client        kubernetes.Interface
	namespace     string
	leaseDuration time.Duration
}

func NewManager(client kubernetes.Interface, namespace string, duration time.Duration) *Manager {
	return &Manager{
		client:        client,
		namespace:     namespace,
		leaseDuration: duration,
	}
}

// Renew creates or updates the Lease for the given AZ with holderID.
func (m *Manager) Renew(ctx context.Context, az, holderID string) error {
	name := leaseName(az)
	now := metav1.NewMicroTime(time.Now())
	durSecs := int32(m.leaseDuration.Seconds())

	existing, err := m.client.CoordinationV1().Leases(m.namespace).
		Get(ctx, name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		_, err = m.client.CoordinationV1().Leases(m.namespace).Create(ctx,
			&coordinationv1.Lease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: m.namespace,
					Labels:    map[string]string{"app": "kube-nat"},
				},
				Spec: coordinationv1.LeaseSpec{
					HolderIdentity:       &holderID,
					LeaseDurationSeconds: &durSecs,
					RenewTime:            &now,
				},
			}, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return fmt.Errorf("get lease %s: %w", name, err)
	}
	existing.Spec.HolderIdentity = &holderID
	existing.Spec.RenewTime = &now
	existing.Spec.LeaseDurationSeconds = &durSecs
	_, err = m.client.CoordinationV1().Leases(m.namespace).
		Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

// Acquire attempts to take ownership of another AZ's Lease.
// Returns true if successful, false if another agent already holds it.
func (m *Manager) Acquire(ctx context.Context, az, holderID string) (bool, error) {
	name := leaseName(az)
	now := metav1.NewMicroTime(time.Now())
	durSecs := int32(m.leaseDuration.Seconds())

	existing, err := m.client.CoordinationV1().Leases(m.namespace).
		Get(ctx, name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		_, err = m.client.CoordinationV1().Leases(m.namespace).Create(ctx,
			&coordinationv1.Lease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: m.namespace,
					Labels:    map[string]string{"app": "kube-nat"},
				},
				Spec: coordinationv1.LeaseSpec{
					HolderIdentity:       &holderID,
					LeaseDurationSeconds: &durSecs,
					RenewTime:            &now,
				},
			}, metav1.CreateOptions{})
		if err != nil {
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
	if !IsExpired(existing, time.Now()) {
		return false, nil
	}
	existing.Spec.HolderIdentity = &holderID
	existing.Spec.RenewTime = &now
	_, err = m.client.CoordinationV1().Leases(m.namespace).
		Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		if errors.IsConflict(err) {
			// Another agent updated the lease between our Get and Update — clean loss.
			return false, nil
		}
		return false, err
	}
	return true, nil
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

func leaseName(az string) string      { return "kube-nat-" + az }
func azFromLeaseName(name string) string {
	const prefix = "kube-nat-"
	if len(name) > len(prefix) {
		return name[len(prefix):]
	}
	return ""
}
