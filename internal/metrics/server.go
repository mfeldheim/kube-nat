package metrics

import (
	"context"
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

// AddClaimHandler registers POST /claim on mux. fn is called with the request context
// and should trigger an immediate route table claim for this agent.
func AddClaimHandler(mux *http.ServeMux, fn func(ctx context.Context) error) {
	mux.HandleFunc("/claim", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := fn(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})
}

// AddReleaseHandler registers POST /release on mux. fn is called with the request context
// and should restore route tables to their original NAT gateways.
func AddReleaseHandler(mux *http.ServeMux, fn func(ctx context.Context) error) {
	mux.HandleFunc("/release", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := fn(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})
}

