package metrics

import (
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Handler returns an HTTP handler for the Prometheus metrics endpoint.
func Handler(reg *Registry) http.Handler {
	return promhttp.HandlerFor(reg.Prometheus(), promhttp.HandlerOpts{})
}

// ListenAddr returns the address string for the given port.
func ListenAddr(port int) string {
	return fmt.Sprintf(":%d", port)
}

// NewMux returns an http.ServeMux with /metrics, /healthz, /readyz registered.
// readyFn is called on /readyz — return nil when ready, error when not.
func NewMux(reg *Registry, readyFn func() error) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/metrics", Handler(reg))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := readyFn(); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})
	return mux
}
