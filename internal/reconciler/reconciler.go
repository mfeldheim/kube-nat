package reconciler

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"sync"

	kubenataws "github.com/kube-nat/kube-nat/internal/aws"
	"github.com/kube-nat/kube-nat/internal/nat"
)

type Config struct {
	NATManager nat.Manager
	EC2Client  kubenataws.EC2Client
	Iface      string
	AZ         string
	InstanceID string
	Region     string
	Mode       string // "auto" or "manual"
	LogWriter  io.Writer
}

type Reconciler struct {
	cfg    Config
	logger *log.Logger

	mu          sync.RWMutex
	ownedTables []string // RTB IDs this agent currently owns (auto mode only)
}

func New(cfg Config) *Reconciler {
	w := cfg.LogWriter
	if w == nil {
		w = os.Stderr
	}
	return &Reconciler{
		cfg:    cfg,
		logger: log.New(w, "[reconciler] ", log.LstdFlags),
	}
}

// OwnedTables returns the route table IDs this agent currently owns.
// Empty in manual mode or before the first successful claim.
func (r *Reconciler) OwnedTables() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.ownedTables))
	copy(out, r.ownedTables)
	return out
}

// Reconcile brings the node into the desired NAT state.
// Safe to call repeatedly — all operations are idempotent.
func (r *Reconciler) Reconcile(ctx context.Context) error {
	if err := r.cfg.NATManager.EnsureMasquerade(r.cfg.Iface); err != nil {
		return fmt.Errorf("ensure masquerade: %w", err)
	}
	if err := r.cfg.NATManager.EnableIPForward(); err != nil {
		return fmt.Errorf("ip_forward: %w", err)
	}

	tables, err := r.cfg.EC2Client.DiscoverRouteTables(ctx, r.cfg.AZ)
	if err != nil {
		return fmt.Errorf("discover route tables: %w", err)
	}
	if len(tables) == 0 {
		r.logger.Printf("no route tables found for az=%s (missing kube-nat/managed=true tag?)", r.cfg.AZ)
		return nil
	}

	region := r.cfg.Region
	if region == "" {
		region = "<region>"
	}

	var owned []string
	for _, rt := range tables {
		if r.cfg.Mode == "manual" {
			r.logger.Printf("[MANUAL] aws ec2 replace-route --route-table-id %s --destination-cidr-block 0.0.0.0/0 --instance-id %s --region %s",
				rt.ID, r.cfg.InstanceID, region)
			continue
		}
		if err := r.cfg.EC2Client.ClaimRouteTable(ctx, rt.ID, r.cfg.InstanceID); err != nil {
			return fmt.Errorf("claim route table %s: %w", rt.ID, err)
		}
		r.logger.Printf("claimed route table %s", rt.ID)
		owned = append(owned, rt.ID)
	}

	if len(owned) > 0 {
		r.mu.Lock()
		r.ownedTables = owned
		r.mu.Unlock()
	}
	return nil
}

// ClaimRouteTables discovers and immediately claims route tables for this AZ,
// regardless of the configured mode. Used by the manual-claim HTTP endpoint.
func (r *Reconciler) ClaimRouteTables(ctx context.Context) error {
	tables, err := r.cfg.EC2Client.DiscoverRouteTables(ctx, r.cfg.AZ)
	if err != nil {
		return fmt.Errorf("discover route tables: %w", err)
	}
	if len(tables) == 0 {
		return fmt.Errorf("no route tables found for az=%s (missing kube-nat/managed=true tag?)", r.cfg.AZ)
	}

	var owned []string
	for _, rt := range tables {
		if err := r.cfg.EC2Client.ClaimRouteTable(ctx, rt.ID, r.cfg.InstanceID); err != nil {
			return fmt.Errorf("claim route table %s: %w", rt.ID, err)
		}
		r.logger.Printf("manually claimed route table %s", rt.ID)
		owned = append(owned, rt.ID)
	}

	r.mu.Lock()
	r.ownedTables = owned
	r.mu.Unlock()
	return nil
}
