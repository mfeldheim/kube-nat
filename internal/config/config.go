package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Mode              string
	AZLabel           string
	LeaseDuration     time.Duration
	ProbeInterval     time.Duration
	ProbeFailures     int
	ReconcileInterval time.Duration
	MetricsPort       int
	PeerPort          int
	ConntrackMax      int
	IPLocalPortRange  string
	TagPrefix         string
	DiscoveryValue    string // when set, filters route tables by tagPrefix/discovery=value
	Namespace         string
	ScrapeInterval    time.Duration
	DashboardPort     int
}

func Load() (*Config, error) {
	cfg := &Config{
		Mode:              getEnv("KUBE_NAT_MODE", "auto"),
		AZLabel:           getEnv("KUBE_NAT_AZ_LABEL", "topology.kubernetes.io/zone"),
		LeaseDuration:     getDurationEnv("KUBE_NAT_LEASE_DURATION", 15*time.Second),
		ProbeInterval:     getDurationEnv("KUBE_NAT_PROBE_INTERVAL", 200*time.Millisecond),
		ProbeFailures:     getIntEnv("KUBE_NAT_PROBE_FAILURES", 2),
		ReconcileInterval: getDurationEnv("KUBE_NAT_RECONCILE_INTERVAL", 30*time.Second),
		MetricsPort:       getIntEnv("KUBE_NAT_METRICS_PORT", 9100),
		PeerPort:          getIntEnv("KUBE_NAT_PEER_PORT", 9101),
		ConntrackMax:      getIntEnv("KUBE_NAT_CONNTRACK_MAX", 0),
		IPLocalPortRange:  getEnv("KUBE_NAT_IP_LOCAL_PORT_RANGE", ""),
		TagPrefix:         getEnv("KUBE_NAT_TAG_PREFIX", "kube-nat"),
		DiscoveryValue:    getEnv("KUBE_NAT_DISCOVERY", ""),
		Namespace:         getEnv("POD_NAMESPACE", "kube-system"),
		ScrapeInterval:    getDurationEnv("KUBE_NAT_SCRAPE_INTERVAL", 5*time.Second),
		DashboardPort:     getIntEnv("KUBE_NAT_DASHBOARD_PORT", 8080),
	}
	if cfg.Mode != "auto" && cfg.Mode != "manual" {
		return nil, fmt.Errorf("KUBE_NAT_MODE must be 'auto' or 'manual', got %q", cfg.Mode)
	}
	return cfg, nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getIntEnv(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getDurationEnv(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
