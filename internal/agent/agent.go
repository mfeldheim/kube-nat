package agent

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
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

	// Fetch and expose peak network bandwidth for this instance type.
	if maxBps, err := ec2Client.DescribeInstanceMaxBandwidth(ctx, meta.InstanceType); err != nil {
		logger.Printf("WARNING: could not determine max bandwidth for %s: %v", meta.InstanceType, err)
	} else {
		reg.MaxBandwidthBps.Set(maxBps)
		logger.Printf("instance type=%s peak bandwidth=%.0f bps (%.1f Gbps)", meta.InstanceType, maxBps, maxBps/1e9)
	}

	// Expose instance type and version as labels so the dashboard can display them.
	reg.InstanceInfo.WithLabelValues(meta.InstanceType, cfg.ServerVersion).Set(1)

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
	logger.Printf("initializing lease manager namespace=%s duration=%s home-az=%s", cfg.Namespace, cfg.LeaseDuration, meta.AZ)
	leaseMgr := lease.NewManager(k8sClient, cfg.Namespace, cfg.LeaseDuration, meta.AZ)

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

	// 9. Data plane — iptables MASQUERADE + IP forwarding. No route table is
	// claimed yet: the peer server (below) must be listening first, so peers
	// never observe a route owned by an agent they cannot reach.
	logger.Printf("applying data plane (iptables + ip_forward)")
	if err := rec.SetupDataPlane(); err != nil {
		return fmt.Errorf("initial data plane setup: %w", err)
	}

	// 10. Peer server — listen before claiming anything, so peers never observe
	// a route owned by an agent they cannot reach. The peer subsystem has its
	// own context so it stays up long enough to broadcast a step-down during
	// shutdown (the main context is already cancelled by then).
	peerCtx, peerCancel := context.WithCancel(context.Background())
	defer peerCancel()
	peerAddr := fmt.Sprintf(":%d", cfg.PeerPort)
	peerSrv := peer.NewServer(peerAddr)
	if err := peerSrv.Listen(); err != nil {
		return fmt.Errorf("peer server listen: %w", err)
	}
	go peerSrv.Serve(peerCtx)
	logger.Printf("peer server on %s", peerAddr)

	// 11. HTTP server: /metrics /healthz /readyz /claim /release. Started early
	// (so /healthz answers during boot) but /readyz reports 503 until the data
	// plane is applied and this AZ's route table is claimed.
	var ready atomic.Bool
	readyFn := func() error {
		if !ready.Load() {
			return fmt.Errorf("not ready")
		}
		return nil
	}
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

	// 12. Claim our own AZ's route table. This is always safe: the target is
	// this node's own instance ID, so it is identical no matter which pod of
	// this AZ issues the write — the own-AZ claim can never flap against another
	// agent. The data plane and peer server are already up, so peers never
	// observe a route owned by an agent they cannot reach.
	podName := podNameOrInstanceID(meta.InstanceID)
	if err := rec.ClaimOwnRouteTables(ctx); err != nil {
		logger.Printf("initial route table claim failed (will retry): %v", err)
	} else {
		ready.Store(true)
	}

	// 13. Establish our own AZ's Lease (best-effort; the lease loop maintains
	// it). The Lease is the lock that lets a failover agent take over an AZ
	// whose owner is truly gone, and lets us reclaim ours from a failover
	// holder without stealing from a live owner.
	if ok, err := leaseMgr.Acquire(ctx, meta.AZ, podName); err != nil {
		logger.Printf("initial own lease acquire: %v", err)
	} else if ok {
		logger.Printf("acquired own lease az=%s", meta.AZ)
	}

	// 14. Peer watcher — discovers other agents and monitors their health.
	// Runs periodically so agents that start after us are still discovered.
	var (
		peerMu      sync.Mutex
		peerClients []*peer.Client
	)
	go startPeerWatcher(peerCtx, cfg, k8sClient, leaseMgr, ec2Client, meta, reg, logger, rec, &peerMu, &peerClients)

	// 15. Spot watcher — proactive step-down on interruption notice. It only
	// cancels the context: the shutdown path releases Leases *before* telling
	// peers to take over (ordering matters, see the shutdown block).
	spotWatcher := spot.NewWatcher("", time.Second, func() {
		logger.Printf("spot interruption notice — initiating step-down")
		reg.SpotInterruptionPending.Set(1)
		cancel()
	})
	go spotWatcher.Run(ctx)

	// 16. Lease renewal loop — the lock heartbeat. Renews own and failover
	// Leases every LeaseRenewInterval (a fraction of LeaseDuration) so a healthy
	// owner's Lease never spuriously expires and is never falsely stolen.
	//
	// Losing our own Lease does NOT make us stop serving our own AZ (the own-AZ
	// route target is always this instance, so continuing is safe and correct);
	// it just means a failover agent currently holds the lock, so we try to
	// reclaim it. Losing a *failover* Lease means the AZ's real owner is back,
	// so we yield that AZ's bookkeeping.
	go func() {
		ticker := time.NewTicker(cfg.LeaseRenewInterval())
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				held, err := leaseMgr.Renew(ctx, meta.AZ, podName)
				if err != nil {
					logger.Printf("lease renew (own az) error: %v", err)
				}
				if !held {
					if ok, aerr := leaseMgr.Acquire(ctx, meta.AZ, podName); aerr != nil {
						logger.Printf("re-acquire own lease az=%s: %v", meta.AZ, aerr)
					} else if ok {
						logger.Printf("reclaimed own lease az=%s from failover holder", meta.AZ)
					}
				}
				// Keep failover Leases alive; yield any the original owner took back.
				for _, az := range rec.ForeignAZs() {
					ok, rerr := leaseMgr.Renew(ctx, az, podName)
					if rerr != nil {
						logger.Printf("failover lease renew error az=%s: %v", az, rerr)
						continue
					}
					if !ok {
						logger.Printf("failover lease az=%s reclaimed by its owner — yielding", az)
						for _, rtbID := range rec.ReleaseForeignAZ(az) {
							reg.RouteTableOwned.WithLabelValues(rtbID).Set(0)
						}
					}
				}
			}
		}
	}()

	// 17. Reconciliation loop — re-applies the data plane and always re-asserts
	// our own AZ's route table (its target is this instance, so it can never
	// flap), releases failover tables the original owner reclaimed, and runs a
	// last-resort sweep that covers any AZ whose Lease has expired.
	go func() {
		ticker := time.NewTicker(cfg.ReconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				logger.Printf("reconcile tick")
				if err := rec.SetupDataPlane(); err != nil {
					logger.Printf("data plane reconcile error: %v", err)
					ready.Store(false)
					continue
				}
				if err := rec.ClaimOwnRouteTables(ctx); err != nil {
					logger.Printf("own route table reconcile error: %v", err)
					ready.Store(false)
				} else {
					ready.Store(true)
				}
				// Detect when original owners reclaim their route tables.
				if vacated, err := rec.ReconcileForeign(ctx); err != nil {
					logger.Printf("reconcile foreign error: %v", err)
				} else {
					for _, rtbID := range vacated {
						reg.RouteTableOwned.WithLabelValues(rtbID).Set(0)
					}
				}
				// Last-resort catch-all: cover any AZ whose Lease has expired and
				// that we are not already covering.
				if expired, err := leaseMgr.ListExpiredAZs(ctx, meta.AZ); err != nil {
					logger.Printf("list expired leases: %v", err)
				} else {
					for _, az := range expired {
						if rec.CoversAZ(az) {
							continue
						}
						logger.Printf("lease for az=%s expired — attempting takeover", az)
						takeover(ctx, cfg, leaseMgr, ec2Client, meta, reg, logger, az, rec)
					}
				}
				updateMetrics(reg, meta, natMgr, rec)
			}
		}
	}()

	// 18. Bandwidth + resource sampler — 1s ticker for bandwidth, CPU, and memory.
	go func() {
		type samplerState struct {
			bytesTX uint64
			bytesRX uint64
			txEMA   float64
			rxEMA   float64
			cpuEMA  float64
			ts      time.Time
			cpu     iface.CPUStat
		}
		var prev samplerState
		// Seed CPU baseline so the first delta is valid.
		if c, err := iface.ReadCPUStat(); err == nil {
			prev.cpu = c
		}
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				ifStats, ifErr := iface.GetStats(meta.PublicIface)
				curCPU, cpuErr := iface.ReadCPUStat()
				if !prev.ts.IsZero() {
					elapsed := now.Sub(prev.ts).Seconds()
					const halfLife = 20.0
					alpha := 1 - math.Exp(-elapsed/halfLife)
					if ifErr == nil {
						tx, rx := ifStats.BytesTX, ifStats.BytesRX
						if tx >= prev.bytesTX && rx >= prev.bytesRX {
							rawTx := float64(tx-prev.bytesTX) / elapsed
							rawRx := float64(rx-prev.bytesRX) / elapsed
							txEMA := alpha*rawTx + (1-alpha)*prev.txEMA
							rxEMA := alpha*rawRx + (1-alpha)*prev.rxEMA
							reg.TxBytesPerSec.WithLabelValues(meta.AZ, meta.InstanceID).Set(txEMA)
							reg.RxBytesPerSec.WithLabelValues(meta.AZ, meta.InstanceID).Set(rxEMA)
							prev.txEMA = txEMA
							prev.rxEMA = rxEMA
						}
					}
					if cpuErr == nil {
						rawCPU := iface.CPURatio(prev.cpu, curCPU)
						cpuEMA := alpha*rawCPU + (1-alpha)*prev.cpuEMA
						reg.CPUUsageRatio.Set(cpuEMA)
						prev.cpuEMA = cpuEMA
					}
				}
				if ifErr == nil {
					prev.bytesTX = ifStats.BytesTX
					prev.bytesRX = ifStats.BytesRX
				}
				if cpuErr == nil {
					prev.cpu = curCPU
				}
				prev.ts = now

				// Memory is instantaneous — read every tick.
				if mem, err := iface.ReadMemStat(); err == nil {
					reg.MemUsedBytes.Set(float64(mem.UsedBytes()))
					reg.MemTotalBytes.Set(float64(mem.TotalBytes))
				}
			}
		}
	}()

	logger.Printf("ready — az=%s instance=%s mode=%s", meta.AZ, meta.InstanceID, cfg.Mode)
	<-ctx.Done()

	logger.Printf("shutting down — releasing leases then stepping down peers")
	ready.Store(false)

	// Release the Lease FIRST. A failover agent's Acquire is gated on the Lease
	// being absent or expired, so releasing before the step-down is what lets it
	// take over immediately instead of losing a race and waiting a full interval.
	relCtx, relCancel := context.WithTimeout(context.Background(), 3*time.Second)
	if err := leaseMgr.Release(relCtx, meta.AZ, podName); err != nil {
		logger.Printf("release own lease: %v", err)
	}
	for _, az := range rec.ForeignAZs() {
		if err := leaseMgr.Release(relCtx, az, podName); err != nil {
			logger.Printf("release failover lease az=%s: %v", az, err)
		}
	}
	relCancel()

	peerMu.Lock()
	for _, c := range peerClients {
		c.SendStepDown()
	}
	peerMu.Unlock()
	time.Sleep(200 * time.Millisecond) // allow step-down to propagate
	peerCancel()                       // tear down the peer subsystem last
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
	rec *reconciler.Reconciler, mu *sync.Mutex, clients *[]*peer.Client) {

	var azMu sync.Mutex
	connectedAZs := make(map[string]bool)
	// staleInstances tracks the instance ID of a peer that failed or stepped down,
	// so its metric label set can be deleted when the replacement is discovered.
	staleInstances := make(map[string]string) // az → old instanceID

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
				// If a previous instance for this AZ had failed, clean up its stale metric.
				if staleID := staleInstances[podAZ]; staleID != "" && staleID != pod.Spec.NodeName {
					reg.PeerStatus.DeleteLabelValues(podAZ, staleID)
					delete(staleInstances, podAZ)
				}
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
			reg.FailoverTotal.WithLabelValues(peerAZ, meta.AZ)
			reg.FailoverTotal.WithLabelValues(meta.AZ, peerAZ)

			c := peer.NewClient(peerAZ, peer.ClientConfig{
				ProbeInterval: cfg.ProbeInterval,
				ProbeFailures: cfg.ProbeFailures,
				OnFailure: func(az string) {
					reg.PeerStatus.WithLabelValues(az, peerInstanceID).Set(0)
					logger.Printf("peer %s declared dead — attempting takeover", az)
					azMu.Lock()
					delete(connectedAZs, az)
					staleInstances[az] = peerInstanceID
					azMu.Unlock()
					takeover(ctx, cfg, leaseMgr, ec2Client, meta, reg, logger, az, rec)
				},
				OnStepDown: func(az string) {
					logger.Printf("peer %s stepping down — taking over", az)
					azMu.Lock()
					delete(connectedAZs, az)
					staleInstances[az] = peerInstanceID
					azMu.Unlock()
					takeover(ctx, cfg, leaseMgr, ec2Client, meta, reg, logger, az, rec)
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
	reg *metrics.Registry, logger *log.Logger, deadAZ string, rec *reconciler.Reconciler) {

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

	var claimed []string
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
			claimed = append(claimed, rt.ID)
		}
	}

	if len(claimed) > 0 {
		rec.AddForeignTables(deadAZ, claimed)
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
	if rec.CanFallbackToNAT() {
		reg.FallbackAvailable.Set(1)
	} else {
		reg.FallbackAvailable.Set(0)
	}
}

func podNameOrInstanceID(instanceID string) string {
	if n := os.Getenv("POD_NAME"); n != "" {
		return n
	}
	return instanceID
}
