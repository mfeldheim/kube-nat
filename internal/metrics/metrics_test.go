package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kube-nat/kube-nat/internal/metrics"
)

func TestMetricsRegistered(t *testing.T) {
	reg := metrics.NewRegistry()
	if reg == nil {
		t.Fatal("nil registry")
	}
}

func TestMetricsHTTPHandler(t *testing.T) {
	reg := metrics.NewRegistry()
	handler := metrics.Handler(reg)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("want 200 got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "kube_nat_") {
		t.Error("want kube_nat_ metrics in response")
	}
}

func TestHealthzAlwaysOK(t *testing.T) {
	reg := metrics.NewRegistry()
	mux := metrics.NewMux(reg, func() error { return nil })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200 got %d", resp.StatusCode)
	}
}
