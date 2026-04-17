package nat

import (
	"fmt"

	"github.com/coreos/go-iptables/iptables"
)

// Manager abstracts iptables operations for testing.
type Manager interface {
	EnsureMasquerade(iface string) error
	MasqueradeExists(iface string) (bool, error)
	EnableIPForward() error
	SetConntrackMax(max int) error
}

type iptablesManager struct {
	ipt *iptables.IPTables
}

// NewManager returns a real iptables-backed Manager.
// Requires NET_ADMIN capability.
func NewManager() (Manager, error) {
	ipt, err := iptables.New()
	if err != nil {
		return nil, fmt.Errorf("iptables init: %w", err)
	}
	return &iptablesManager{ipt: ipt}, nil
}

func (m *iptablesManager) EnsureMasquerade(iface string) error {
	rule := []string{"-o", iface, "-j", "MASQUERADE",
		"-m", "comment", "--comment", "kube-nat managed rule"}
	exists, err := m.ipt.Exists("nat", "POSTROUTING", rule...)
	if err != nil {
		return fmt.Errorf("check rule: %w", err)
	}
	if exists {
		return nil
	}
	return m.ipt.Append("nat", "POSTROUTING", rule...)
}

func (m *iptablesManager) MasqueradeExists(iface string) (bool, error) {
	rule := []string{"-o", iface, "-j", "MASQUERADE",
		"-m", "comment", "--comment", "kube-nat managed rule"}
	return m.ipt.Exists("nat", "POSTROUTING", rule...)
}

func (m *iptablesManager) EnableIPForward() error {
	return writeSysctl("/proc/sys/net/ipv4/ip_forward", "1")
}

func (m *iptablesManager) SetConntrackMax(max int) error {
	if max <= 0 {
		return nil
	}
	return writeSysctl("/proc/sys/net/netfilter/nf_conntrack_max", fmt.Sprintf("%d", max))
}
