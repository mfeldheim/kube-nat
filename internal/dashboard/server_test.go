package dashboard_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kube-nat/kube-nat/internal/dashboard"
	"k8s.io/client-go/kubernetes/fake"
)

func TestHealthzReturns200(t *testing.T) {
	k8s := fake.NewSimpleClientset()
	srv := dashboard.NewServer(dashboard.Config{
		K8sClient:      k8s,
		Namespace:      "kube-system",
		MetricsPort:    9100,
		ScrapeInterval: 5,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200 got %d", resp.StatusCode)
	}
}

func TestRootServesSPA(t *testing.T) {
	k8s := fake.NewSimpleClientset()
	srv := dashboard.NewServer(dashboard.Config{
		K8sClient:      k8s,
		Namespace:      "kube-system",
		MetricsPort:    9100,
		ScrapeInterval: 5,
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200 got %d", resp.StatusCode)
	}
}
