package nat

import (
	"fmt"
	"os"
)

func writeSysctl(path, value string) error {
	if err := os.WriteFile(path, []byte(value), 0644); err != nil {
		return fmt.Errorf("sysctl %s=%s: %w", path, value, err)
	}
	return nil
}

// SetPortRange writes net.ipv4.ip_local_port_range. range format: "1024 65535"
func SetPortRange(portRange string) error {
	if portRange == "" {
		return nil
	}
	return writeSysctl("/proc/sys/net/ipv4/ip_local_port_range", portRange)
}
