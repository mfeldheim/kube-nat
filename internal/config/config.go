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
	ServerVersion     string
}

func Load() (*Config, error) {
	cfg := &Config{
		Mode:              getEnv("KUBE_NAT_MODE", "auto"),
		AZLabel:           getEnv("KUBE_NAT_AZ_LABEL", "topology.kubernetes.io/zone"),
		LeaseDuration:     getDurationEnv("KUBE_NAT_LEASE_DURATION", 30*time.Second),
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
		ServerVersion:     getEnv("KUBE_NAT_SERVER_VERSION", "dev"),
	}
	if cfg.Mode != "auto" && cfg.Mode != "manual" {
		return nil, fmt.Errorf("KUBE_NAT_MODE must be 'auto' or 'manual', got %q", cfg.Mode)
	}
	if cfg.LeaseDuration <= 0 {
		return nil, fmt.Errorf("KUBE_NAT_LEASE_DURATION must be positive, got %v", cfg.LeaseDuration)
	}
	if cfg.ReconcileInterval <= 0 {
		return nil, fmt.Errorf("KUBE_NAT_RECONCILE_INTERVAL must be positive, got %v", cfg.ReconcileInterval)
	}
	return cfg, nil
}

// LeaseRenewInterval is how often the agent renews its Leases. It is derived so
// that the renewal happens several times within LeaseDuration — a healthy
// owner's Lease therefore never spuriously expires, which is what made peers
// steal healthy AZs before. It is also capped at ReconcileInterval so the
// renewal loop is at least as frequent as the reconcile loop.
func (c *Config) LeaseRenewInterval() time.Duration {
	d := c.LeaseDuration / 4
	if d < time.Second {
		d = time.Second
	}
	if c.ReconcileInterval > 0 && d > c.ReconcileInterval {
		d = c.ReconcileInterval
	}
	return d
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
