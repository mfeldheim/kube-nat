package nat

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/coreos/go-iptables/iptables"
)

// Manager abstracts iptables operations for testing.
type Manager interface {
	EnsureMasquerade(iface string) error
	MasqueradeExists(iface string) (bool, error)
	EnableIPForward() error
	SetConntrackMax(max int) error
	EnsureStaleFlowReset(iface string) error
}

// iptablesAPI is the part of go-iptables the manager uses, so rule placement
// can be tested without a kernel.
type iptablesAPI interface {
	Exists(table, chain string, rulespec ...string) (bool, error)
	Append(table, chain string, rulespec ...string) error
	Insert(table, chain string, pos int, rulespec ...string) error
	Delete(table, chain string, rulespec ...string) error
	List(table, chain string) ([]string, error)
}

type iptablesManager struct {
	ipt iptablesAPI
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
	return writeSysctl("/host/proc/sys/net/ipv4/ip_forward", "1")
}

func (m *iptablesManager) SetConntrackMax(max int) error {
	if max <= 0 {
		return nil
	}
	return writeSysctl("/host/proc/sys/net/netfilter/nf_conntrack_max", fmt.Sprintf("%d", max))
}

// staleFlowResetComment marks the FORWARD rule that answers packets of
// connections this node never saw with a TCP reset.
const staleFlowResetComment = "kube-nat stale flow reset"

func staleFlowResetRule(iface string) []string {
	return []string{"-o", iface, "-p", "tcp",
		"-m", "conntrack", "--ctstate", "INVALID",
		"-m", "comment", "--comment", staleFlowResetComment,
		"-j", "REJECT", "--reject-with", "tcp-reset"}
}

// EnsureStaleFlowReset makes this node reset connections it has no conntrack
// state for. After a NAT swap, pods keep sending on connections that were
// translated by the previous NAT node. By default (nf_conntrack_tcp_loose=1)
// the new node adopts those mid-stream packets and sends them out under its
// own public IP, where the remote end silently drops them, so the client
// retransmits until tcp_retries2 gives up (about 15 minutes). With loose
// pickup off the packets are INVALID, and the REJECT answers each one with a
// RST built from the packet itself, so the client reconnects at once.
//
// The rule has to be the first one in FORWARD: kube-proxy's KUBE-FORWARD
// drops INVALID packets silently. The rule goes in before loose pickup is
// turned off, so INVALID packets are never left to that DROP.
func (m *iptablesManager) EnsureStaleFlowReset(iface string) error {
	rule := staleFlowResetRule(iface)
	first, err := m.isFirstForwardRule()
	if err != nil {
		return err
	}
	if !first {
		exists, err := m.ipt.Exists("filter", "FORWARD", rule...)
		if err != nil {
			return fmt.Errorf("check stale flow rule: %w", err)
		}
		if exists {
			if err := m.ipt.Delete("filter", "FORWARD", rule...); err != nil {
				return fmt.Errorf("move stale flow rule: %w", err)
			}
		}
		if err := m.ipt.Insert("filter", "FORWARD", 1, rule...); err != nil {
			return fmt.Errorf("insert stale flow rule: %w", err)
		}
	}
	return writeSysctl(filepath.Join(procSysRoot, "net", "netfilter", "nf_conntrack_tcp_loose"), "0")
}

// isFirstForwardRule reports whether the stale flow rule is the first rule in
// filter/FORWARD (policy lines are skipped).
func (m *iptablesManager) isFirstForwardRule() (bool, error) {
	rules, err := m.ipt.List("filter", "FORWARD")
	if err != nil {
		return false, fmt.Errorf("list FORWARD: %w", err)
	}
	for _, r := range rules {
		if strings.HasPrefix(r, "-P ") {
			continue
		}
		return strings.Contains(r, staleFlowResetComment), nil
	}
	return false, nil
}
