package agent

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	kubenataws "github.com/kube-nat/kube-nat/internal/aws"
	"github.com/kube-nat/kube-nat/internal/config"
	"github.com/kube-nat/kube-nat/internal/iface"
	"github.com/kube-nat/kube-nat/internal/lease"
	"github.com/kube-nat/kube-nat/internal/metrics"
	"github.com/kube-nat/kube-nat/internal/nat"
	"github.com/kube-nat/kube-nat/internal/peer"
	"github.com/kube-nat/kube-nat/internal/reconciler"
	"github.com/kube-nat/kube-nat/internal/spot"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func Run(cfg *config.Config) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	logger := log.New(os.Stderr, "[agent] ", log.LstdFlags)

	// 1. Fetch EC2 metadata
	metaClient := kubenataws.NewMetadataClient("")
	meta, err := metaClient.Fetch(ctx)
	if err != nil {
		return fmt.Errorf("fetch metadata: %w", err)
	}
	logger.Printf("instance=%s az=%s eni=%s iface=%s", meta.InstanceID, meta.AZ, meta.ENIID, meta.PublicIface)

	// 2. AWS EC2 client (IRSA credentials injected via pod env)
	logger.Printf("initializing AWS EC2 client region=%s tag-prefix=%s discovery=%q", meta.Region, cfg.TagPrefix, cfg.DiscoveryValue)
	ec2Client, err := kubenataws.NewEC2Client(ctx, meta.Region, cfg.TagPrefix, cfg.DiscoveryValue)
	if err != nil {
		return fmt.Errorf("aws ec2 client: %w", err)
	}
	logger.Printf("AWS EC2 client ready")

	// 3. Disable src/dst check on primary ENI
	logger.Printf("disabling src/dst check on ENI %s", meta.ENIID)
	if err := ec2Client.DisableSourceDestCheck(ctx, meta.ENIID); err != nil {
		return fmt.Errorf("disable src/dst check: %w", err)
	}
	logger.Printf("src/dst check disabled on %s", meta.ENIID)

	// 4. NAT manager
	logger.Printf("initializing NAT manager conntrack-max=%d port-range=%q", cfg.ConntrackMax, cfg.IPLocalPortRange)
	natMgr, err := nat.NewManager()
	if err != nil {
		return fmt.Errorf("nat manager: %w", err)
	}
	if err := natMgr.SetConntrackMax(cfg.ConntrackMax); err != nil {
		return fmt.Errorf("conntrack max: %w", err)
	}
	if err := nat.SetPortRange(cfg.IPLocalPortRange); err != nil {
		return fmt.Errorf("port range: %w", err)
	}
	logger.Printf("NAT manager ready")

	// 5. Metrics
	logger.Printf("initializing metrics registry")
	reg := metrics.NewRegistry()
	reg.SrcDstCheckDisabled.Set(1)
	// Initialize byte/packet counters with instance labels so they appear in
	// /metrics from startup — the dashboard reads the az/instance_id labels
	// from these series to identify each agent, even before traffic flows.
	reg.BytesTX.WithLabelValues(meta.AZ, meta.InstanceID, meta.PublicIface)
	reg.BytesRX.WithLabelValues(meta.AZ, meta.InstanceID, meta.PublicIface)
	reg.PacketsTX.WithLabelValues(meta.AZ, meta.InstanceID, meta.PublicIface)
	reg.PacketsRX.WithLabelValues(meta.AZ, meta.InstanceID, meta.PublicIface)

	// 6. Kubernetes client (in-cluster)
	logger.Printf("initializing Kubernetes in-cluster client")
	k8sCfg, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("k8s in-cluster config: %w", err)
	}
	k8sClient, err := kubernetes.NewForConfig(k8sCfg)
	if err != nil {
		return fmt.Errorf("k8s client: %w", err)
	}
	logger.Printf("Kubernetes client ready")

	// 7. Lease manager
	logger.Printf("initializing lease manager namespace=%s duration=%s", cfg.Namespace, cfg.LeaseDuration)
	leaseMgr := lease.NewManager(k8sClient, cfg.Namespace, cfg.LeaseDuration)

	// 8. Reconciler
	logger.Printf("initializing reconciler mode=%s iface=%s az=%s instance=%s", cfg.Mode, meta.PublicIface, meta.AZ, meta.InstanceID)
	rec := reconciler.New(reconciler.Config{
		NATManager: natMgr,
		EC2Client:  ec2Client,
		Iface:      meta.PublicIface,
		AZ:         meta.AZ,
		InstanceID: meta.InstanceID,
		Region:     meta.Region,
		Mode:       cfg.Mode,
	})

	// 9. Initial reconcile — sets up iptables and claims route table
	logger.Printf("running initial reconcile")
	if err := rec.Reconcile(ctx); err != nil {
		return fmt.Errorf("initial reconcile: %w", err)
	}
	logger.Printf("initial reconcile complete")

	// 10. Write own Lease
	podName := podNameOrInstanceID(meta.InstanceID)
	logger.Printf("writing initial lease az=%s pod=%s", meta.AZ, podName)
	if err := leaseMgr.Renew(ctx, meta.AZ, podName); err != nil {
		return fmt.Errorf("initial lease renew: %w", err)
	}
	logger.Printf("lease written")

	// 11. Peer server
	peerAddr := fmt.Sprintf(":%d", cfg.PeerPort)
	peerSrv := peer.NewServer(peerAddr)
	if err := peerSrv.Listen(); err != nil {
		return fmt.Errorf("peer server listen: %w", err)
	}
	go peerSrv.Serve(ctx)
	logger.Printf("peer server on %s", peerAddr)

	// 12. Peer watcher — discovers other agents and monitors their health.
	// Runs periodically so agents that start after us are still discovered.
	var (
		peerMu      sync.Mutex
		peerClients []*peer.Client
	)
	go startPeerWatcher(ctx, cfg, k8sClient, leaseMgr, ec2Client, meta, reg, logger, &peerMu, &peerClients)

	// 13. Spot watcher — proactive step-down on interruption notice
	spotWatcher := spot.NewWatcher("", time.Second, func() {
		logger.Printf("spot interruption notice — initiating step-down")
		reg.SpotInterruptionPending.Set(1)
		peerMu.Lock()
		for _, c := range peerClients {
			c.SendStepDown()
		}
		peerMu.Unlock()
		cancel()
	})
	go spotWatcher.Run(ctx)

	// 14. Ready gate
	ready := true
	readyFn := func() error {
		if !ready {
			return fmt.Errorf("not ready")
		}
		return nil
	}

	// 15. HTTP server: /metrics /healthz /readyz /claim /release
	mux := metrics.NewMux(reg, readyFn)
	metrics.AddClaimHandler(mux, func(ctx context.Context) error {
		logger.Printf("manual route table claim triggered via HTTP")
		return rec.ClaimRouteTables(ctx)
	})
	metrics.AddReleaseHandler(mux, func(ctx context.Context) error {
		logger.Printf("route table release (fallback to NAT gateway) triggered via HTTP")
		return rec.ReleaseRouteTables(ctx)
	})
	httpSrv := &http.Server{
		Addr:    metrics.ListenAddr(cfg.MetricsPort),
		Handler: mux,
	}
	go func() {
		logger.Printf("metrics on %s", httpSrv.Addr)
		if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
			logger.Printf("metrics server: %v", err)
		}
	}()

	// 16. Reconciliation loop
	go func() {
		ticker := time.NewTicker(cfg.ReconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				logger.Printf("reconcile tick")
				if err := rec.Reconcile(ctx); err != nil {
					logger.Printf("reconcile error: %v", err)
				}
				if err := leaseMgr.Renew(ctx, meta.AZ, podName); err != nil {
					logger.Printf("lease renew error: %v", err)
				}
				updateMetrics(reg, meta, natMgr, rec)
			}
		}
	}()

	logger.Printf("ready — az=%s instance=%s mode=%s", meta.AZ, meta.InstanceID, cfg.Mode)
	<-ctx.Done()

	logger.Printf("shutting down — sending step-down to peers")
	ready = false
	peerMu.Lock()
	for _, c := range peerClients {
		c.SendStepDown()
	}
	peerMu.Unlock()
	time.Sleep(100 * time.Millisecond) // allow step-down to propagate
	httpSrv.Shutdown(context.Background())
	return nil
}

// startPeerWatcher runs a periodic pod-list loop, connecting to any peer agent
// pods that haven't been seen before. This ensures agents that start after us
// are still discovered and monitored. connectedAZs tracks which AZs already
// have an active client so we don't create duplicates.
//
// When a peer client declares failure (OnFailure), the AZ is removed from
// connectedAZs so the next discovery tick creates a fresh client — this
// handles peers that temporarily go down during rolling updates.
func startPeerWatcher(ctx context.Context, cfg *config.Config, k8sClient kubernetes.Interface,
	leaseMgr *lease.Manager, ec2Client kubenataws.EC2Client,
	meta *kubenataws.InstanceMetadata, reg *metrics.Registry, logger *log.Logger,
	mu *sync.Mutex, clients *[]*peer.Client) {

	var azMu sync.Mutex
	connectedAZs := make(map[string]bool)

	discover := func() {
		podList, err := k8sClient.CoreV1().Pods(cfg.Namespace).List(ctx, metav1.ListOptions{
			LabelSelector: "app=kube-nat,component=agent",
		})
		if err != nil {
			logger.Printf("peer discovery: list pods: %v", err)
			return
		}
		for _, pod := range podList.Items {
			podAZ := pod.Labels[cfg.AZLabel]
			if podAZ == meta.AZ || pod.Status.PodIP == "" {
				continue
			}
			azMu.Lock()
			already := connectedAZs[podAZ]
			if !already {
				connectedAZs[podAZ] = true
			}
			azMu.Unlock()
			if already {
				continue
			}

			peerAddr := fmt.Sprintf("%s:%d", pod.Status.PodIP, cfg.PeerPort)
			peerAZ := podAZ
			peerInstanceID := pod.Spec.NodeName

			logger.Printf("discovered peer az=%s instance=%s addr=%s", peerAZ, peerInstanceID, peerAddr)
			reg.PeerStatus.WithLabelValues(peerAZ, peerInstanceID).Set(1)

			c := peer.NewClient(peerAZ, peer.ClientConfig{
				ProbeInterval: cfg.ProbeInterval,
				ProbeFailures: cfg.ProbeFailures,
				OnFailure: func(az string) {
					reg.PeerStatus.WithLabelValues(az, peerInstanceID).Set(0)
					logger.Printf("peer %s declared dead — attempting takeover", az)
					// Remove from connectedAZs so the next discovery tick can reconnect
					// if the peer restarts (e.g. rolling update).
					azMu.Lock()
					delete(connectedAZs, az)
					azMu.Unlock()
					takeover(ctx, cfg, leaseMgr, ec2Client, meta, reg, logger, az)
				},
				OnStepDown: func(az string) {
					logger.Printf("peer %s stepping down — taking over", az)
					azMu.Lock()
					delete(connectedAZs, az)
					azMu.Unlock()
					takeover(ctx, cfg, leaseMgr, ec2Client, meta, reg, logger, az)
				},
			})
			mu.Lock()
			*clients = append(*clients, c)
			mu.Unlock()
			go c.Connect(ctx, peerAddr)
		}
	}

	// Run immediately, then on every reconcile interval.
	discover()
	ticker := time.NewTicker(cfg.ReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			discover()
		}
	}
}

func takeover(ctx context.Context, cfg *config.Config, leaseMgr *lease.Manager,
	ec2Client kubenataws.EC2Client, meta *kubenataws.InstanceMetadata,
	reg *metrics.Registry, logger *log.Logger, deadAZ string) {

	podName := podNameOrInstanceID(meta.InstanceID)
	acquired, err := leaseMgr.Acquire(ctx, deadAZ, podName)
	if err != nil {
		logger.Printf("takeover %s: acquire lease: %v", deadAZ, err)
		return
	}
	if !acquired {
		logger.Printf("takeover %s: another agent won the lease race", deadAZ)
		return
	}

	tables, err := ec2Client.DiscoverRouteTables(ctx, deadAZ)
	if err != nil {
		logger.Printf("takeover %s: discover route tables: %v", deadAZ, err)
		return
	}

	for _, rt := range tables {
		if cfg.Mode == "manual" {
			logger.Printf("[MANUAL] aws ec2 replace-route --route-table-id %s --destination-cidr-block 0.0.0.0/0 --instance-id %s --region %s",
				rt.ID, meta.InstanceID, meta.Region)
			continue
		}
		if err := ec2Client.ClaimRouteTable(ctx, rt.ID, meta.InstanceID); err != nil {
			logger.Printf("takeover %s: claim %s: %v", deadAZ, rt.ID, err)
		} else {
			logger.Printf("takeover %s: claimed %s", deadAZ, rt.ID)
			reg.RouteTableOwned.WithLabelValues(rt.ID).Set(1)
		}
	}

	reg.FailoverTotal.WithLabelValues(deadAZ, meta.AZ).Inc()
	reg.LastFailover.WithLabelValues(deadAZ).Set(float64(time.Now().Unix()))
}

func updateMetrics(reg *metrics.Registry, meta *kubenataws.InstanceMetadata, natMgr nat.Manager, rec *reconciler.Reconciler) {
	exists, err := natMgr.MasqueradeExists(meta.PublicIface)
	if err == nil {
		v := 0.0
		if exists {
			v = 1.0
		}
		reg.RulePresent.WithLabelValues("MASQUERADE").Set(v)
	}
	count, max, err := iface.ConntrackStats()
	if err == nil && max > 0 {
		reg.ConntrackEntries.Set(float64(count))
		reg.ConntrackMax.Set(float64(max))
		reg.ConntrackUsageRatio.Set(float64(count) / float64(max))
	}
	// Update route table ownership gauge with real RTB IDs.
	for _, rtbID := range rec.OwnedTables() {
		reg.RouteTableOwned.WithLabelValues(rtbID).Set(1)
	}
}

func podNameOrInstanceID(instanceID string) string {
	if n := os.Getenv("POD_NAME"); n != "" {
		return n
	}
	return instanceID
}
