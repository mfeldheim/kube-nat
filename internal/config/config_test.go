package config_test

import (
	"testing"
	"time"

	"github.com/kube-nat/kube-nat/internal/config"
)

func TestDefaults(t *testing.T) {
	t.Setenv("KUBE_NAT_MODE", "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "auto" {
		t.Errorf("want Mode=auto got %s", cfg.Mode)
	}
	if cfg.ProbeInterval != 200*time.Millisecond {
		t.Errorf("want ProbeInterval=200ms got %v", cfg.ProbeInterval)
	}
	if cfg.ProbeFailures != 2 {
		t.Errorf("want ProbeFailures=2 got %d", cfg.ProbeFailures)
	}
	if cfg.MetricsPort != 9100 {
		t.Errorf("want MetricsPort=9100 got %d", cfg.MetricsPort)
	}
	if cfg.PeerPort != 9101 {
		t.Errorf("want PeerPort=9101 got %d", cfg.PeerPort)
	}
	if cfg.TagPrefix != "kube-nat" {
		t.Errorf("want TagPrefix=kube-nat got %s", cfg.TagPrefix)
	}
}

func TestManualMode(t *testing.T) {
	t.Setenv("KUBE_NAT_MODE", "manual")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "manual" {
		t.Errorf("want manual got %s", cfg.Mode)
	}
}

func TestInvalidMode(t *testing.T) {
	t.Setenv("KUBE_NAT_MODE", "bad")
	_, err := config.Load()
	if err == nil {
		t.Error("want error for invalid mode")
	}
}

func TestDashboardDefaults(t *testing.T) {
	t.Setenv("KUBE_NAT_MODE", "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ScrapeInterval != 5*time.Second {
		t.Errorf("want 5s got %v", cfg.ScrapeInterval)
	}
	if cfg.DashboardPort != 8080 {
		t.Errorf("want 8080 got %d", cfg.DashboardPort)
	}
}
