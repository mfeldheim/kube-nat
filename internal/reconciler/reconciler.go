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

	mu                 sync.RWMutex
	ownedTables        []string          // RTB IDs this agent currently owns (own AZ)
	foreignOwnedTables map[string]string // rtbID → deadAZ for tables claimed during failover
	originalNatGW      map[string]string // rtbID → nat gateway ID captured at claim time
	rtbVpcID           map[string]string // rtbID → VPC ID (for fallback nat GW lookup)
}

func New(cfg Config) *Reconciler {
	w := cfg.LogWriter
	if w == nil {
		w = os.Stderr
	}
	return &Reconciler{
		cfg:                cfg,
		logger:             log.New(w, "[reconciler] ", log.LstdFlags),
		foreignOwnedTables: make(map[string]string),
		originalNatGW:      make(map[string]string),
		rtbVpcID:           make(map[string]string),
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

// AddForeignTables registers route tables claimed on behalf of a dead AZ.
// These are monitored by ReconcileForeign to detect when the original owner recovers.
func (r *Reconciler) AddForeignTables(deadAZ string, rtbIDs []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range rtbIDs {
		r.foreignOwnedTables[id] = deadAZ
	}
}

// ForeignAZs returns the set of AZs whose route tables this agent is currently covering.
func (r *Reconciler) ForeignAZs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	azSet := make(map[string]bool)
	for _, az := range r.foreignOwnedTables {
		azSet[az] = true
	}
	result := make([]string, 0, len(azSet))
	for az := range azSet {
		result = append(result, az)
	}
	return result
}

// ReconcileForeign checks route tables claimed during failover and detects when
// the original owner has reclaimed them. Returns RTB IDs that are no longer
// owned by this agent so callers can clear the ownership gauge.
func (r *Reconciler) ReconcileForeign(ctx context.Context) ([]string, error) {
	r.mu.RLock()
	foreign := make(map[string]string, len(r.foreignOwnedTables))
	for k, v := range r.foreignOwnedTables {
		foreign[k] = v
	}
	r.mu.RUnlock()

	if len(foreign) == 0 {
		return nil, nil
	}

	// Collect unique AZs to minimise DiscoverRouteTables calls.
	azTables := make(map[string][]string) // az → []rtbID we think we own there
	for rtbID, az := range foreign {
		azTables[az] = append(azTables[az], rtbID)
	}

	var vacated []string
	for az := range azTables {
		tables, err := r.cfg.EC2Client.DiscoverRouteTables(ctx, az)
		if err != nil {
			r.logger.Printf("ReconcileForeign: discover route tables az=%s: %v", az, err)
			continue
		}
		for _, rt := range tables {
			if _, owned := foreign[rt.ID]; !owned {
				continue
			}
			if rt.InstanceID != r.cfg.InstanceID {
				r.logger.Printf("ReconcileForeign: route table %s in az=%s reclaimed by %q — releasing ownership", rt.ID, az, rt.InstanceID)
				vacated = append(vacated, rt.ID)
				r.mu.Lock()
				delete(r.foreignOwnedTables, rt.ID)
				r.mu.Unlock()
			}
		}
	}
	return vacated, nil
}

// Reconcile brings the node into the desired NAT state.
// Safe to call repeatedly — all operations are idempotent.
// In auto mode: verifies each owned route table points to this instance and re-claims if not.
// In manual mode: logs the current route state and the command needed to fix any drift, without acting.
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
		r.logger.Printf("no route tables found for az=%s (check kube-nat/managed=true or kube-nat/discovery tag)", r.cfg.AZ)
		return nil
	}

	region := r.cfg.Region
	if region == "" {
		region = "<region>"
	}

	var confirmed []string
	for _, rt := range tables {
		if r.cfg.Mode == "manual" {
			if rt.InstanceID == r.cfg.InstanceID {
				r.logger.Printf("[MANUAL] route table %s already points at this instance — ok", rt.ID)
			} else {
				current := rt.NatGatewayID
				if rt.InstanceID != "" {
					current = rt.InstanceID
				}
				r.logger.Printf("[MANUAL] route table %s points at %s (not us) — aws ec2 replace-route --route-table-id %s --destination-cidr-block 0.0.0.0/0 --instance-id %s --region %s",
					rt.ID, current, rt.ID, r.cfg.InstanceID, region)
			}
			continue
		}

		// Auto mode: re-claim if the route doesn't already point to us.
		if rt.InstanceID == r.cfg.InstanceID {
			r.logger.Printf("route table %s already points at this instance — verified", rt.ID)
		} else {
			current := rt.NatGatewayID
			if rt.InstanceID != "" {
				current = rt.InstanceID
			}
			r.logger.Printf("route table %s points at %s — claiming for this instance", rt.ID, current)
			if err := r.cfg.EC2Client.ClaimRouteTable(ctx, rt.ID, r.cfg.InstanceID); err != nil {
				r.logger.Printf("claim route table %s: %v", rt.ID, err)
				return fmt.Errorf("claim route table %s: %w", rt.ID, err)
			}
			r.logger.Printf("claimed route table %s", rt.ID)
		}
		confirmed = append(confirmed, rt.ID)

		// Record VPC ID and original NAT GW for fallback (only on first observation).
		r.mu.Lock()
		if rt.VpcID != "" {
			r.rtbVpcID[rt.ID] = rt.VpcID
		}
		if rt.NatGatewayID != "" {
			if _, exists := r.originalNatGW[rt.ID]; !exists {
				r.originalNatGW[rt.ID] = rt.NatGatewayID
			}
		}
		r.mu.Unlock()
	}

	r.mu.Lock()
	if r.cfg.Mode != "manual" {
		r.ownedTables = confirmed
	}
	r.mu.Unlock()
	return nil
}

// ClaimRouteTables discovers and immediately claims route tables for this AZ,
// regardless of the configured mode. Used by the manual-claim HTTP endpoint.
func (r *Reconciler) ClaimRouteTables(ctx context.Context) error {
	tables, err := r.cfg.EC2Client.DiscoverRouteTables(ctx, r.cfg.AZ)
	if err != nil {
		r.logger.Printf("ClaimRouteTables: discover: %v", err)
		return fmt.Errorf("discover route tables: %w", err)
	}
	if len(tables) == 0 {
		err := fmt.Errorf("no route tables found for az=%s (check kube-nat/managed=true or kube-nat/discovery tag)", r.cfg.AZ)
		r.logger.Printf("ClaimRouteTables: %v", err)
		return err
	}

	var owned []string
	for _, rt := range tables {
		if err := r.cfg.EC2Client.ClaimRouteTable(ctx, rt.ID, r.cfg.InstanceID); err != nil {
			r.logger.Printf("ClaimRouteTables: claim %s: %v", rt.ID, err)
			return fmt.Errorf("claim route table %s: %w", rt.ID, err)
		}
		r.logger.Printf("manually claimed route table %s", rt.ID)
		owned = append(owned, rt.ID)
	}

	r.mu.Lock()
	r.ownedTables = owned
	for _, rt := range tables {
		if rt.VpcID != "" {
			r.rtbVpcID[rt.ID] = rt.VpcID
		}
		if rt.NatGatewayID != "" {
			if _, exists := r.originalNatGW[rt.ID]; !exists {
				r.originalNatGW[rt.ID] = rt.NatGatewayID
			}
		}
	}
	r.mu.Unlock()
	return nil
}

// ReleaseRouteTables restores the 0.0.0.0/0 routes to their original NAT gateways.
// Used by the fallback HTTP endpoint to hand routing back to AWS NAT GW.
func (r *Reconciler) ReleaseRouteTables(ctx context.Context) error {
	r.mu.RLock()
	owned := make([]string, len(r.ownedTables))
	copy(owned, r.ownedTables)
	natGWs := make(map[string]string, len(r.originalNatGW))
	for k, v := range r.originalNatGW {
		natGWs[k] = v
	}
	vpcIDs := make(map[string]string, len(r.rtbVpcID))
	for k, v := range r.rtbVpcID {
		vpcIDs[k] = v
	}
	r.mu.RUnlock()

	if len(owned) == 0 {
		r.logger.Printf("ReleaseRouteTables: no owned tables to release")
		return nil
	}

	for _, rtbID := range owned {
		natGwID := natGWs[rtbID]
		if natGwID == "" {
			// Not recorded in memory (e.g. agent restarted while route pointed at us).
			// Look up the active NAT gateway for this VPC+AZ live from AWS.
			vpcID := vpcIDs[rtbID]
			if vpcID == "" {
				r.logger.Printf("ReleaseRouteTables: no vpc ID known for %s — cannot look up nat gateway, skipping", rtbID)
				continue
			}
			r.logger.Printf("ReleaseRouteTables: no cached nat gateway for %s — looking up in vpc=%s az=%s", rtbID, vpcID, r.cfg.AZ)
			found, err := r.cfg.EC2Client.LookupNatGateway(ctx, vpcID, r.cfg.AZ)
			if err != nil {
				r.logger.Printf("ReleaseRouteTables: lookup nat gateway for %s: %v", rtbID, err)
				return fmt.Errorf("lookup nat gateway for %s: %w", rtbID, err)
			}
			natGwID = found
		}
		if err := r.cfg.EC2Client.ReleaseRouteTable(ctx, rtbID, natGwID); err != nil {
			r.logger.Printf("ReleaseRouteTables: release %s: %v", rtbID, err)
			return fmt.Errorf("release route table %s: %w", rtbID, err)
		}
		r.logger.Printf("released route table %s → nat gateway %s", rtbID, natGwID)
	}

	r.mu.Lock()
	r.ownedTables = nil
	r.mu.Unlock()
	return nil
}
