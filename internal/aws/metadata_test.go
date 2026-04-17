package aws_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	kubenataws "github.com/kube-nat/kube-nat/internal/aws"
)

func newIMDSServer() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/latest/api/token", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("test-token"))
	})
	mux.HandleFunc("/latest/meta-data/instance-id", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("i-0abc123def456"))
	})
	mux.HandleFunc("/latest/meta-data/placement/availability-zone", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("eu-west-1a"))
	})
	mux.HandleFunc("/latest/meta-data/placement/region", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("eu-west-1"))
	})
	mux.HandleFunc("/latest/meta-data/mac", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("0a:1b:2c:3d:4e:5f"))
	})
	mux.HandleFunc("/latest/meta-data/network/interfaces/macs/0a:1b:2c:3d:4e:5f/interface-id", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("eni-0abc123"))
	})
	mux.HandleFunc("/latest/meta-data/network/interfaces/macs/0a:1b:2c:3d:4e:5f/device-number", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("0"))
	})
	mux.HandleFunc("/latest/meta-data/spot/termination-time", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	return httptest.NewServer(mux)
}

func TestFetchMetadata(t *testing.T) {
	srv := newIMDSServer()
	defer srv.Close()

	client := kubenataws.NewMetadataClient(srv.URL)
	meta, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if meta.InstanceID != "i-0abc123def456" {
		t.Errorf("want i-0abc123def456 got %s", meta.InstanceID)
	}
	if meta.AZ != "eu-west-1a" {
		t.Errorf("want eu-west-1a got %s", meta.AZ)
	}
	if meta.Region != "eu-west-1" {
		t.Errorf("want eu-west-1 got %s", meta.Region)
	}
	if meta.ENIID != "eni-0abc123" {
		t.Errorf("want eni-0abc123 got %s", meta.ENIID)
	}
	if meta.PublicIface != "eth0" {
		t.Errorf("want eth0 (device 0) got %s", meta.PublicIface)
	}
}

func TestSpotTerminationNotPresent(t *testing.T) {
	srv := newIMDSServer()
	defer srv.Close()

	client := kubenataws.NewMetadataClient(srv.URL)
	_, pending, err := client.SpotTerminationTime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Error("want pending=false when no termination notice")
	}
}
