package nat

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/coreos/go-iptables/iptables"
)

// Manager abstracts iptables operations for testing.
type Manager interface {
	EnsureMasquerade(iface string) error
	MasqueradeExists(iface string) (bool, error)
	EnableIPForward() error
	SetConntrackMax(max int) error
	// EnsureForwardCounters inserts two byte-counting rules in filter FORWARD to
	// separately track TX (original/upload direction) and RX (reply/download direction).
	// These rules use conntrack direction matching, so they correctly differentiate traffic
	// even on a single-NIC NAT instance where eth0 handles both directions.
	EnsureForwardCounters() error
	// GetForwardBytes returns the accumulated byte totals from the forward counting rules.
	// TX = original direction (client→internet), RX = reply direction (internet→client).
	GetForwardBytes() (tx uint64, rx uint64, err error)
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
	return writeSysctl("/host/proc/sys/net/ipv4/ip_forward", "1")
}

func (m *iptablesManager) SetConntrackMax(max int) error {
	if max <= 0 {
		return nil
	}
	return writeSysctl("/host/proc/sys/net/netfilter/nf_conntrack_max", fmt.Sprintf("%d", max))
}

const (
	fwdTXComment = "kube-nat tx"
	fwdRXComment = "kube-nat rx"
)

func (m *iptablesManager) EnsureForwardCounters() error {
	// TX rule: original direction (client→internet / upload).
	txRule := []string{"-m", "conntrack", "--ctdir", "ORIGINAL",
		"-m", "comment", "--comment", fwdTXComment, "-j", "RETURN"}
	if exists, err := m.ipt.Exists("filter", "FORWARD", txRule...); err != nil {
		return fmt.Errorf("check tx forward rule: %w", err)
	} else if !exists {
		if err := m.ipt.Insert("filter", "FORWARD", 1, txRule...); err != nil {
			return fmt.Errorf("insert tx forward rule: %w", err)
		}
	}

	// RX rule: reply direction (internet→client / download).
	rxRule := []string{"-m", "conntrack", "--ctdir", "REPLY",
		"-m", "comment", "--comment", fwdRXComment, "-j", "RETURN"}
	if exists, err := m.ipt.Exists("filter", "FORWARD", rxRule...); err != nil {
		return fmt.Errorf("check rx forward rule: %w", err)
	} else if !exists {
		if err := m.ipt.Insert("filter", "FORWARD", 2, rxRule...); err != nil {
			return fmt.Errorf("insert rx forward rule: %w", err)
		}
	}
	return nil
}

func (m *iptablesManager) GetForwardBytes() (tx uint64, rx uint64, err error) {
	rows, err := m.ipt.Stats("filter", "FORWARD")
	if err != nil {
		return 0, 0, fmt.Errorf("iptables stats: %w", err)
	}
	for _, row := range rows {
		// Stats row format: [pkts, bytes, target, prot, opt, in, out, src, dst, options...]
		if len(row) < 10 {
			continue
		}
		opts := strings.Join(row[9:], " ")
		var dest *uint64
		if strings.Contains(opts, fwdTXComment) {
			dest = &tx
		} else if strings.Contains(opts, fwdRXComment) {
			dest = &rx
		}
		if dest != nil {
			if v, parseErr := strconv.ParseUint(row[1], 10, 64); parseErr == nil {
				*dest = v
			}
		}
	}
	return tx, rx, nil
}
