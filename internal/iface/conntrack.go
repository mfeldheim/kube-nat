package iface

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const defaultConntrackCountPath = "/proc/sys/net/netfilter/nf_conntrack_count"
const defaultConntrackMaxPath = "/proc/sys/net/netfilter/nf_conntrack_max"

// ReadConntrackCount reads the current number of tracked connections from path.
func ReadConntrackCount(path string) (int, error) {
	return readIntFile(path)
}

// ReadConntrackMax reads the maximum connection tracking limit from path.
func ReadConntrackMax(path string) (int, error) {
	return readIntFile(path)
}

// ConntrackStats returns current count and max from /proc.
func ConntrackStats() (count, max int, err error) {
	count, err = ReadConntrackCount(defaultConntrackCountPath)
	if err != nil {
		return 0, 0, fmt.Errorf("conntrack count: %w", err)
	}
	max, err = ReadConntrackMax(defaultConntrackMaxPath)
	if err != nil {
		return 0, 0, fmt.Errorf("conntrack max: %w", err)
	}
	return count, max, nil
}

func readIntFile(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", path, err)
	}
	return n, nil
}
