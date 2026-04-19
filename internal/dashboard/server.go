package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kube-nat/kube-nat/internal/collector"
	webui "github.com/kube-nat/kube-nat/web"
	"nhooyr.io/websocket"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Config configures the dashboard server.
type Config struct {
	K8sClient      kubernetes.Interface
	Namespace      string
	MetricsPort    int
	ScrapeInterval int // seconds
}

// Server is the dashboard HTTP server.
type Server struct {
	cfg       Config
	hub       *Hub
	collector *collector.Collector
	logger    *log.Logger
}

// NewServer creates a Server.
func NewServer(cfg Config) *Server {
	col := collector.New(collector.Config{
		K8sClient:      cfg.K8sClient,
		Namespace:      cfg.Namespace,
		MetricsPort:    cfg.MetricsPort,
		ScrapeInterval: time.Duration(cfg.ScrapeInterval) * time.Second,
	})
	return &Server{
		cfg:       cfg,
		hub:       NewHub(),
		collector: col,
		logger:    log.New(os.Stderr, "[dashboard] ", log.LstdFlags),
	}
}

// Handler returns the HTTP handler (useful for testing without starting the server).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Serve embedded SPA. Sub into dist/ so /index.html and /assets/... work.
	sub, err := fs.Sub(webui.FS, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	mux.Handle("/", fileServer)

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/agents/", s.handleAgentClaim)

	return mux
}

// Run starts the collector loop and HTTP server. Blocks until ctx is done.
func (s *Server) Run(ctx context.Context, addr string) error {
	// Start collector loop — push Snapshot to all WS clients every ScrapeInterval.
	go func() {
		interval := time.Duration(s.cfg.ScrapeInterval) * time.Second
		if interval == 0 {
			interval = 5 * time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				snap, err := s.collector.Collect(ctx)
				if err != nil {
					s.logger.Printf("collect error: %v", err)
					continue
				}
				b, err := json.Marshal(snap)
				if err != nil {
					s.logger.Printf("marshal error: %v", err)
					continue
				}
				s.hub.Broadcast(ctx, b)
			}
		}
	}()

	srv := &http.Server{Addr: addr, Handler: s.Handler()}
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()
	s.logger.Printf("listening on %s", addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// handleAgentClaim handles POST /agents/{az}/claim.
// It looks up the agent pod for the given AZ and proxies a POST /claim to it.
func (s *Server) handleAgentClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// URL: /agents/{az}/claim
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/agents/"), "/")
	if len(parts) < 2 || parts[1] != "claim" {
		http.NotFound(w, r)
		return
	}
	az := parts[0]
	if az == "" {
		http.Error(w, "missing az", http.StatusBadRequest)
		return
	}

	pods, err := s.cfg.K8sClient.CoreV1().Pods(s.cfg.Namespace).List(r.Context(), metav1.ListOptions{
		LabelSelector: "app=kube-nat,component=agent",
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("list pods: %v", err), http.StatusInternalServerError)
		return
	}

	var podIP string
	for _, pod := range pods.Items {
		if pod.Labels["topology.kubernetes.io/zone"] == az && pod.Status.PodIP != "" {
			podIP = pod.Status.PodIP
			break
		}
	}
	if podIP == "" {
		http.Error(w, fmt.Sprintf("no agent pod found for az %s", az), http.StatusNotFound)
		return
	}

	agentURL := fmt.Sprintf("http://%s:%d/claim", podIP, s.cfg.MetricsPort)
	resp, err := http.Post(agentURL, "application/json", nil) //nolint:noctx
	if err != nil {
		http.Error(w, fmt.Sprintf("claim request failed: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, fmt.Sprintf("agent returned %d", resp.StatusCode), http.StatusBadGateway)
		return
	}

	s.collector.AddEvent(collector.EventEntry{
		TS:     time.Now().UnixMilli(),
		AZ:     az,
		Kind:   "route_claimed",
		Detail: fmt.Sprintf("manual route table claim triggered for %s", az),
	})

	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ok")
}
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // allow any Origin for kubectl port-forward use
	})
	if err != nil {
		s.logger.Printf("ws accept: %v", err)
		return
	}
	c := &client{conn: conn}
	s.hub.register(c)
	defer s.hub.unregister(c)

	// Send current snapshot immediately so browser doesn't wait for the first tick.
	snap, err := s.collector.Collect(r.Context())
	if err == nil {
		if b, err := json.Marshal(snap); err == nil {
			conn.Write(r.Context(), websocket.MessageText, b)
		}
	}

	// Block until client disconnects.
	for {
		if _, _, err := conn.Read(r.Context()); err != nil {
			return
		}
	}
}
