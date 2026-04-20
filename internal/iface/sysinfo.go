package iface

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	hostProcStat    = "/host/proc/stat"
	hostProcMeminfo = "/host/proc/meminfo"
)

// CPUStat holds raw cumulative CPU ticks from /proc/stat.
type CPUStat struct {
	User    uint64
	Nice    uint64
	System  uint64
	Idle    uint64
	IOWait  uint64
	IRQ     uint64
	SoftIRQ uint64
	Steal   uint64
}

func (s CPUStat) Total() uint64 {
	return s.User + s.Nice + s.System + s.Idle + s.IOWait + s.IRQ + s.SoftIRQ + s.Steal
}

func (s CPUStat) Busy() uint64 {
	return s.Total() - s.Idle - s.IOWait
}

// CPURatio computes the fraction of CPU time that was busy between two samples.
// Returns 0 if the samples are identical (no elapsed time).
func CPURatio(prev, cur CPUStat) float64 {
	totalDelta := float64(cur.Total() - prev.Total())
	if totalDelta <= 0 {
		return 0
	}
	busyDelta := float64(cur.Busy() - prev.Busy())
	if busyDelta < 0 {
		busyDelta = 0
	}
	return busyDelta / totalDelta
}

// ReadCPUStat reads the aggregate CPU line from /proc/stat.
func ReadCPUStat() (CPUStat, error) {
	f, err := os.Open(hostProcStat)
	if err != nil {
		return CPUStat{}, fmt.Errorf("open %s: %w", hostProcStat, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 8 {
			return CPUStat{}, fmt.Errorf("unexpected cpu line: %q", line)
		}
		parse := func(i int) uint64 {
			v, _ := strconv.ParseUint(fields[i], 10, 64)
			return v
		}
		return CPUStat{
			User:    parse(1),
			Nice:    parse(2),
			System:  parse(3),
			Idle:    parse(4),
			IOWait:  parse(5),
			IRQ:     parse(6),
			SoftIRQ: parse(7),
			Steal:   parse(8),
		}, nil
	}
	return CPUStat{}, fmt.Errorf("cpu line not found in %s", hostProcStat)
}

// MemStat holds total and available memory in bytes.
type MemStat struct {
	TotalBytes     uint64
	AvailableBytes uint64
}

func (m MemStat) UsedBytes() uint64 {
	if m.AvailableBytes > m.TotalBytes {
		return 0
	}
	return m.TotalBytes - m.AvailableBytes
}

// ReadMemStat reads MemTotal and MemAvailable from /proc/meminfo.
func ReadMemStat() (MemStat, error) {
	f, err := os.Open(hostProcMeminfo)
	if err != nil {
		return MemStat{}, fmt.Errorf("open %s: %w", hostProcMeminfo, err)
	}
	defer f.Close()

	var stat MemStat
	found := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() && found < 2 {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		kbVal, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			stat.TotalBytes = kbVal * 1024
			found++
		case "MemAvailable:":
			stat.AvailableBytes = kbVal * 1024
			found++
		}
	}
	if found < 2 {
		return MemStat{}, fmt.Errorf("could not read MemTotal/MemAvailable from %s", hostProcMeminfo)
	}
	return stat, nil
}
