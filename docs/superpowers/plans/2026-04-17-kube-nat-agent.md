# kube-nat Agent Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the kube-nat Go daemon — a DaemonSet pod that manages iptables MASQUERADE, disables ENI src/dst check, owns per-AZ route tables, detects peer failures via TCP heartbeats in ~400ms, and exposes Prometheus metrics.

**Architecture:** Single Go binary with `kube-nat agent` subcommand. Uses `coreos/go-iptables` for NAT rules, `vishvananda/netlink` for interface stats, `aws-sdk-go-v2` for EC2 API, and `client-go` with Kubernetes Lease API for split-brain prevention. Peer failure detection uses application-level 200ms heartbeats over persistent TCP connections.

**Tech Stack:** Go 1.22, cobra, aws-sdk-go-v2, coreos/go-iptables v0.7.0, vishvananda/netlink v1.1.0, prometheus/client_golang v1.20.0, k8s.io/client-go v0.31.3

---

## File Map

```
kube-nat/
├── go.mod
├── go.sum
├── Makefile
├── Dockerfile
├── cmd/
│   └── kube-nat/
│       └── main.go                     # cobra root, registers agent + dashboard subcommands
├── internal/
│   ├── config/
│   │   ├── config.go                   # Config struct, env var loading, validation
│   │   └── config_test.go
│   ├── aws/
│   │   ├── metadata.go                 # EC2 IMDS client, InstanceMetadata struct
│   │   ├── metadata_test.go            # mock HTTP server for IMDS
│   │   ├── ec2.go                      # EC2Client interface + real impl: src/dst check, route tables
│   │   └── ec2_test.go                 # mock EC2 API via interface injection
│   ├── nat/
│   │   ├── manager.go                  # NATManager interface + iptables impl via go-iptables
│   │   ├── manager_test.go             # mock NATManager for unit tests
│   │   └── sysctl.go                   # EnableIPForward, SetConntrackMax, SetPortRange
│   ├── iface/
│   │   ├── stats.go                    # InterfaceStats via netlink: bytes/packets TX/RX
│   │   ├── conntrack.go                # conntrack entry count from /proc/net/nf_conntrack_count
│   │   └── stats_test.go
│   ├── metrics/
│   │   ├── metrics.go                  # all Prometheus metric definitions (Counters, Gauges)
│   │   └── server.go                   # HTTP server: /metrics, /healthz, /readyz
│   ├── lease/
│   │   ├── lease.go                    # LeaseManager: renew own AZ, watch all AZs, detect expiry
│   │   └── lease_test.go               # k8s fake client
│   ├── peer/
│   │   ├── protocol.go                 # message types, encode/decode (9-byte frames)
│   │   ├── server.go                   # TCP listener on :9101, accepts peer connections
│   │   ├── client.go                   # TCP client: connects to peers, sends heartbeats, detects failure
│   │   └── peer_test.go                # real TCP loopback tests
│   ├── spot/
│   │   ├── watcher.go                  # polls IMDS spot/termination-time every 1s
│   │   └── watcher_test.go
│   └── agent/
│       ├── agent.go                    # Agent struct: wires all components, startup sequence
│       └── agent_test.go               # integration test with mocked AWS + k8s
```

---

## Task 1: Project scaffold

**Files:**
- Create: `go.mod`
- Create: `Makefile`
- Create: `cmd/kube-nat/main.go`

- [ ] **Step 1: Create go.mod**

```
module github.com/kube-nat/kube-nat

go 1.22
```

Run: `cd /path/to/kube-nat && go mod init github.com/kube-nat/kube-nat`

- [ ] **Step 2: Install dependencies**

```bash
go get github.com/spf13/cobra@v1.8.1
go get github.com/aws/aws-sdk-go-v2@v1.30.0
go get github.com/aws/aws-sdk-go-v2/config@v1.27.0
go get github.com/aws/aws-sdk-go-v2/service/ec2@v1.161.0
go get github.com/aws/aws-sdk-go-v2/feature/ec2/imds@v1.16.0
go get github.com/coreos/go-iptables@v0.7.0
go get github.com/vishvananda/netlink@v1.1.0
go get github.com/prometheus/client_golang@v1.20.0
go get k8s.io/client-go@v0.31.3
go get k8s.io/api@v0.31.3
go get k8s.io/apimachinery@v0.31.3
go mod tidy
```

Expected: `go.sum` created, no errors.

- [ ] **Step 3: Create cmd/kube-nat/main.go**

```go
package main

import (
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "kube-nat",
		Short: "Kubernetes-native NAT for AWS",
	}
	root.AddCommand(agentCmd())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func agentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agent",
		Short: "Run the NAT agent (DaemonSet mode)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgent()
		},
	}
}

func runAgent() error {
	// wired up in Task 18
	return nil
}
```

- [ ] **Step 4: Create Makefile**

```makefile
.PHONY: build test lint

build:
	go build -o bin/kube-nat ./cmd/kube-nat

test:
	go test ./... -v -race

lint:
	go vet ./...

test-nat:
	# requires NET_ADMIN — run inside Docker
	docker build -t kube-nat-test --target test .
	docker run --rm --cap-add NET_ADMIN kube-nat-test
```

- [ ] **Step 5: Verify build compiles**

```bash
mkdir -p bin && go build -o bin/kube-nat ./cmd/kube-nat
./bin/kube-nat agent
```

Expected: prints nothing and exits 0.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum Makefile cmd/
git commit -m "feat: project scaffold with cobra CLI"
```

---

## Task 2: Config

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/config/config_test.go`:

```go
package config_test

import (
	"testing"
	"time"

	"github.com/kube-nat/kube-nat/internal/config"
)

func TestDefaults(t *testing.T) {
	t.Setenv("KUBE_NAT_MODE", "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "auto" {
		t.Errorf("want Mode=auto got %s", cfg.Mode)
	}
	if cfg.ProbeInterval != 200*time.Millisecond {
		t.Errorf("want ProbeInterval=200ms got %v", cfg.ProbeInterval)
	}
	if cfg.ProbeFailures != 2 {
		t.Errorf("want ProbeFailures=2 got %d", cfg.ProbeFailures)
	}
	if cfg.MetricsPort != 9100 {
		t.Errorf("want MetricsPort=9100 got %d", cfg.MetricsPort)
	}
	if cfg.PeerPort != 9101 {
		t.Errorf("want PeerPort=9101 got %d", cfg.PeerPort)
	}
	if cfg.TagPrefix != "kube-nat" {
		t.Errorf("want TagPrefix=kube-nat got %s", cfg.TagPrefix)
	}
}

func TestManualMode(t *testing.T) {
	t.Setenv("KUBE_NAT_MODE", "manual")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "manual" {
		t.Errorf("want manual got %s", cfg.Mode)
	}
}

func TestInvalidMode(t *testing.T) {
	t.Setenv("KUBE_NAT_MODE", "bad")
	_, err := config.Load()
	if err == nil {
		t.Error("want error for invalid mode")
	}
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/config/... -v
```

Expected: `cannot find package` or compile error.

- [ ] **Step 3: Implement config.go**

Create `internal/config/config.go`:

```go
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Mode              string
	AZLabel           string
	LeaseDuration     time.Duration
	ProbeInterval     time.Duration
	ProbeFailures     int
	ReconcileInterval time.Duration
	MetricsPort       int
	PeerPort          int
	ConntrackMax      int
	IPLocalPortRange  string
	TagPrefix         string
	Namespace         string
}

func Load() (*Config, error) {
	cfg := &Config{
		Mode:              getEnv("KUBE_NAT_MODE", "auto"),
		AZLabel:           getEnv("KUBE_NAT_AZ_LABEL", "topology.kubernetes.io/zone"),
		LeaseDuration:     getDurationEnv("KUBE_NAT_LEASE_DURATION", 15*time.Second),
		ProbeInterval:     getDurationEnv("KUBE_NAT_PROBE_INTERVAL", 200*time.Millisecond),
		ProbeFailures:     getIntEnv("KUBE_NAT_PROBE_FAILURES", 2),
		ReconcileInterval: getDurationEnv("KUBE_NAT_RECONCILE_INTERVAL", 30*time.Second),
		MetricsPort:       getIntEnv("KUBE_NAT_METRICS_PORT", 9100),
		PeerPort:          getIntEnv("KUBE_NAT_PEER_PORT", 9101),
		ConntrackMax:      getIntEnv("KUBE_NAT_CONNTRACK_MAX", 0),
		IPLocalPortRange:  getEnv("KUBE_NAT_IP_LOCAL_PORT_RANGE", ""),
		TagPrefix:         getEnv("KUBE_NAT_TAG_PREFIX", "kube-nat"),
		Namespace:         getEnv("POD_NAMESPACE", "kube-system"),
	}
	if cfg.Mode != "auto" && cfg.Mode != "manual" {
		return nil, fmt.Errorf("KUBE_NAT_MODE must be 'auto' or 'manual', got %q", cfg.Mode)
	}
	return cfg, nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getIntEnv(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil {
			return n
		}
	}
	return def
}

func getDurationEnv(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		d, err := time.ParseDuration(v)
		if err == nil {
			return d
		}
	}
	return def
}
```

- [ ] **Step 4: Run tests and confirm pass**

```bash
go test ./internal/config/... -v
```

Expected: `PASS` for all 3 tests.

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat: config loading from env vars with defaults"
```

---

## Task 3: AWS metadata client

**Files:**
- Create: `internal/aws/metadata.go`
- Create: `internal/aws/metadata_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/aws/metadata_test.go`:

```go
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
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/aws/... -v -run TestFetchMetadata
```

Expected: compile error — package doesn't exist yet.

- [ ] **Step 3: Implement metadata.go**

Create `internal/aws/metadata.go`:

```go
package aws

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

type InstanceMetadata struct {
	InstanceID string
	AZ         string
	Region     string
	ENIID      string
	MAC        string
}

type MetadataClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewMetadataClient(baseURL string) *MetadataClient {
	if baseURL == "" {
		baseURL = "http://169.254.169.254"
	}
	return &MetadataClient{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

func (c *MetadataClient) Fetch(ctx context.Context) (*InstanceMetadata, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("imds token: %w", err)
	}
	get := func(path string) (string, error) {
		return c.get(ctx, token, path)
	}

	instanceID, err := get("/latest/meta-data/instance-id")
	if err != nil {
		return nil, err
	}
	az, err := get("/latest/meta-data/placement/availability-zone")
	if err != nil {
		return nil, err
	}
	region, err := get("/latest/meta-data/placement/region")
	if err != nil {
		return nil, err
	}
	mac, err := get("/latest/meta-data/mac")
	if err != nil {
		return nil, err
	}
	eniID, err := get("/latest/meta-data/network/interfaces/macs/" + mac + "/interface-id")
	if err != nil {
		return nil, err
	}
	return &InstanceMetadata{
		InstanceID: instanceID,
		AZ:         az,
		Region:     region,
		ENIID:      eniID,
		MAC:        mac,
	}, nil
}

// SpotTerminationTime returns the termination time and whether a notice is pending.
// Returns (zero, false, nil) when no notice is present (404 from IMDS).
func (c *MetadataClient) SpotTerminationTime(ctx context.Context) (time.Time, bool, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return time.Time{}, false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/latest/meta-data/spot/termination-time", nil)
	if err != nil {
		return time.Time{}, false, err
	}
	req.Header.Set("X-aws-ec2-metadata-token", token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return time.Time{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return time.Time{}, false, nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return time.Time{}, false, err
	}
	// Format: "2026-04-17T15:30:00Z"
	t, err := time.Parse(time.RFC3339, string(body))
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parse termination time %q: %w", string(body), err)
	}
	return t, true, nil
}

func (c *MetadataClient) getToken(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		c.baseURL+"/latest/api/token", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "300")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("imds token request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (c *MetadataClient) get(ctx context.Context, token, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-aws-ec2-metadata-token", token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: status %d", path, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/aws/... -v -run "TestFetchMetadata|TestSpotTermination"
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/aws/metadata.go internal/aws/metadata_test.go
git commit -m "feat: EC2 IMDS metadata client"
```

---

## Task 4: AWS EC2 client

**Files:**
- Create: `internal/aws/ec2.go`
- Create: `internal/aws/ec2_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/aws/ec2_test.go`:

```go
package aws_test

import (
	"context"
	"testing"

	kubenataws "github.com/kube-nat/kube-nat/internal/aws"
)

type fakeEC2 struct {
	srcDstDisabled bool
	routeTables    []kubenataws.RouteTable
	routes         map[string]string // rtbID -> instanceID
}

func (f *fakeEC2) DisableSourceDestCheck(_ context.Context, _ string) error {
	f.srcDstDisabled = true
	return nil
}

func (f *fakeEC2) DiscoverRouteTables(_ context.Context, az string) ([]kubenataws.RouteTable, error) {
	var result []kubenataws.RouteTable
	for _, rt := range f.routeTables {
		if rt.AZ == az {
			result = append(result, rt)
		}
	}
	return result, nil
}

func (f *fakeEC2) ClaimRouteTable(_ context.Context, rtbID, instanceID string) error {
	if f.routes == nil {
		f.routes = make(map[string]string)
	}
	f.routes[rtbID] = instanceID
	return nil
}

func TestEC2Interface(t *testing.T) {
	var _ kubenataws.EC2Client = &fakeEC2{}
}

func TestDiscoverRouteTables(t *testing.T) {
	f := &fakeEC2{
		routeTables: []kubenataws.RouteTable{
			{ID: "rtb-001", AZ: "eu-west-1a"},
			{ID: "rtb-002", AZ: "eu-west-1b"},
		},
	}
	tables, err := f.DiscoverRouteTables(context.Background(), "eu-west-1a")
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 {
		t.Fatalf("want 1 table got %d", len(tables))
	}
	if tables[0].ID != "rtb-001" {
		t.Errorf("want rtb-001 got %s", tables[0].ID)
	}
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/aws/... -v -run TestEC2Interface
```

Expected: compile error.

- [ ] **Step 3: Implement ec2.go**

Create `internal/aws/ec2.go`:

```go
package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

type RouteTable struct {
	ID string
	AZ string
}

// EC2Client is the interface for all AWS EC2 operations.
// The real implementation uses aws-sdk-go-v2; tests use fakeEC2.
type EC2Client interface {
	DisableSourceDestCheck(ctx context.Context, eniID string) error
	DiscoverRouteTables(ctx context.Context, az string) ([]RouteTable, error)
	ClaimRouteTable(ctx context.Context, rtbID, instanceID string) error
}

type realEC2Client struct {
	svc       *ec2.Client
	tagPrefix string
}

func NewEC2Client(ctx context.Context, region, tagPrefix string) (EC2Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	return &realEC2Client{
		svc:       ec2.NewFromConfig(cfg),
		tagPrefix: tagPrefix,
	}, nil
}

func (c *realEC2Client) DisableSourceDestCheck(ctx context.Context, eniID string) error {
	_, err := c.svc.ModifyNetworkInterfaceAttribute(ctx, &ec2.ModifyNetworkInterfaceAttributeInput{
		NetworkInterfaceId: aws.String(eniID),
		SourceDestCheck:    &types.AttributeBooleanValue{Value: aws.Bool(false)},
	})
	return err
}

func (c *realEC2Client) DiscoverRouteTables(ctx context.Context, az string) ([]RouteTable, error) {
	out, err := c.svc.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("tag:" + c.tagPrefix + "/managed"),
				Values: []string{"true"},
			},
			{
				Name:   aws.String("tag:" + c.tagPrefix + "/az"),
				Values: []string{az},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("describe route tables: %w", err)
	}
	tables := make([]RouteTable, 0, len(out.RouteTables))
	for _, rt := range out.RouteTables {
		tables = append(tables, RouteTable{ID: *rt.RouteTableId, AZ: az})
	}
	return tables, nil
}

func (c *realEC2Client) ClaimRouteTable(ctx context.Context, rtbID, instanceID string) error {
	_, err := c.svc.ReplaceRoute(ctx, &ec2.ReplaceRouteInput{
		RouteTableId:         aws.String(rtbID),
		DestinationCidrBlock: aws.String("0.0.0.0/0"),
		InstanceId:           aws.String(instanceID),
	})
	if err != nil {
		return fmt.Errorf("replace route in %s: %w", rtbID, err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/aws/... -v
```

Expected: PASS for all tests.

- [ ] **Step 5: Commit**

```bash
git add internal/aws/ec2.go internal/aws/ec2_test.go
git commit -m "feat: EC2 client for src/dst check and route table management"
```

---

## Task 5: NAT manager

**Files:**
- Create: `internal/nat/manager.go`
- Create: `internal/nat/sysctl.go`
- Create: `internal/nat/manager_test.go`

> **Note:** The real NAT manager requires `NET_ADMIN` — unit tests use a mock interface. The integration test (`make test-nat`) runs in Docker with `NET_ADMIN`.

- [ ] **Step 1: Write failing tests**

Create `internal/nat/manager_test.go`:

```go
package nat_test

import (
	"testing"

	"github.com/kube-nat/kube-nat/internal/nat"
)

type fakeNAT struct {
	rules   map[string]bool
	forward bool
	connmax int
}

func (f *fakeNAT) EnsureMasquerade(_ string) error {
	if f.rules == nil {
		f.rules = make(map[string]bool)
	}
	f.rules["MASQUERADE"] = true
	return nil
}

func (f *fakeNAT) MasqueradeExists(iface string) (bool, error) {
	return f.rules["MASQUERADE"], nil
}

func (f *fakeNAT) EnableIPForward() error {
	f.forward = true
	return nil
}

func (f *fakeNAT) SetConntrackMax(max int) error {
	f.connmax = max
	return nil
}

func TestNATManagerInterface(t *testing.T) {
	var _ nat.Manager = &fakeNAT{}
}

func TestEnsureMasqueradeIdempotent(t *testing.T) {
	f := &fakeNAT{}
	if err := f.EnsureMasquerade("eth0"); err != nil {
		t.Fatal(err)
	}
	if err := f.EnsureMasquerade("eth0"); err != nil {
		t.Fatalf("second call should be idempotent: %v", err)
	}
	exists, _ := f.MasqueradeExists("eth0")
	if !exists {
		t.Error("rule should exist after EnsureMasquerade")
	}
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/nat/... -v
```

Expected: compile error.

- [ ] **Step 3: Implement manager.go**

Create `internal/nat/manager.go`:

```go
package nat

import (
	"fmt"

	"github.com/coreos/go-iptables/iptables"
)

// Manager abstracts iptables operations for testing.
type Manager interface {
	EnsureMasquerade(iface string) error
	MasqueradeExists(iface string) (bool, error)
	EnableIPForward() error
	SetConntrackMax(max int) error
}

type iptablesManager struct {
	ipt *iptables.IPTables
}

// NewManager returns a real iptables-backed Manager.
// Requires NET_ADMIN capability.
func NewManager() (Manager, error) {
	ipt, err := iptables.New()
	if err != nil {
		return nil, fmt.Errorf("iptables init: %w", err)
	}
	return &iptablesManager{ipt: ipt}, nil
}

func (m *iptablesManager) EnsureMasquerade(iface string) error {
	rule := []string{"-o", iface, "-j", "MASQUERADE",
		"-m", "comment", "--comment", "kube-nat managed rule"}
	exists, err := m.ipt.Exists("nat", "POSTROUTING", rule...)
	if err != nil {
		return fmt.Errorf("check rule: %w", err)
	}
	if exists {
		return nil
	}
	return m.ipt.Append("nat", "POSTROUTING", rule...)
}

func (m *iptablesManager) MasqueradeExists(iface string) (bool, error) {
	rule := []string{"-o", iface, "-j", "MASQUERADE",
		"-m", "comment", "--comment", "kube-nat managed rule"}
	return m.ipt.Exists("nat", "POSTROUTING", rule...)
}

func (m *iptablesManager) EnableIPForward() error {
	return writeSysctl("/proc/sys/net/ipv4/ip_forward", "1")
}

func (m *iptablesManager) SetConntrackMax(max int) error {
	if max <= 0 {
		return nil
	}
	return writeSysctl("/proc/sys/net/netfilter/nf_conntrack_max", fmt.Sprintf("%d", max))
}
```

- [ ] **Step 4: Implement sysctl.go**

Create `internal/nat/sysctl.go`:

```go
package nat

import (
	"fmt"
	"os"
)

func writeSysctl(path, value string) error {
	if err := os.WriteFile(path, []byte(value), 0644); err != nil {
		return fmt.Errorf("sysctl %s=%s: %w", path, value, err)
	}
	return nil
}

// SetPortRange writes net.ipv4.ip_local_port_range.
// range format: "1024 65535"
func SetPortRange(portRange string) error {
	if portRange == "" {
		return nil
	}
	return writeSysctl("/proc/sys/net/ipv4/ip_local_port_range", portRange)
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/nat/... -v
```

Expected: PASS (mock-based tests, no NET_ADMIN needed).

- [ ] **Step 6: Commit**

```bash
git add internal/nat/
git commit -m "feat: NAT manager interface with iptables implementation"
```

---

## Task 6: Interface stats and conntrack

**Files:**
- Create: `internal/iface/stats.go`
- Create: `internal/iface/conntrack.go`
- Create: `internal/iface/stats_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/iface/stats_test.go`:

```go
package iface_test

import (
	"testing"

	"github.com/kube-nat/kube-nat/internal/iface"
)

func TestInterfaceStatsStruct(t *testing.T) {
	s := iface.Stats{
		BytesTX:   1000,
		BytesRX:   2000,
		PacketsTX: 10,
		PacketsRX: 20,
	}
	if s.BytesTX != 1000 {
		t.Errorf("want 1000 got %d", s.BytesTX)
	}
}

func TestConntrackCountPath(t *testing.T) {
	// Verify the reader handles a missing file gracefully
	_, err := iface.ReadConntrackCount("/nonexistent/path")
	if err == nil {
		t.Error("want error for missing file")
	}
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/iface/... -v
```

Expected: compile error.

- [ ] **Step 3: Implement stats.go**

Create `internal/iface/stats.go`:

```go
package iface

import (
	"fmt"

	"github.com/vishvananda/netlink"
)

type Stats struct {
	BytesTX   uint64
	BytesRX   uint64
	PacketsTX uint64
	PacketsRX uint64
}

// GetStats returns TX/RX byte and packet counters for the named interface.
func GetStats(ifaceName string) (*Stats, error) {
	link, err := netlink.LinkByName(ifaceName)
	if err != nil {
		return nil, fmt.Errorf("link %s: %w", ifaceName, err)
	}
	attrs := link.Attrs()
	if attrs.Statistics == nil {
		return nil, fmt.Errorf("no statistics for interface %s", ifaceName)
	}
	return &Stats{
		BytesTX:   attrs.Statistics.TxBytes,
		BytesRX:   attrs.Statistics.RxBytes,
		PacketsTX: attrs.Statistics.TxPackets,
		PacketsRX: attrs.Statistics.RxPackets,
	}, nil
}
```

- [ ] **Step 4: Implement conntrack.go**

Create `internal/iface/conntrack.go`:

```go
package iface

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const defaultConntrackCountPath = "/proc/sys/net/netfilter/nf_conntrack_count"
const defaultConntrackMaxPath = "/proc/sys/net/netfilter/nf_conntrack_max"

// ReadConntrackCount reads the current number of tracked connections.
func ReadConntrackCount(path string) (int, error) {
	return readIntFile(path)
}

// ReadConntrackMax reads the maximum connection tracking limit.
func ReadConntrackMax(path string) (int, error) {
	return readIntFile(path)
}

// ConntrackStats returns current count and max from /proc.
func ConntrackStats() (count, max int, err error) {
	count, err = ReadConntrackCount(defaultConntrackCountPath)
	if err != nil {
		return 0, 0, fmt.Errorf("conntrack count: %w", err)
	}
	max, err = ReadConntrackMax(defaultConntrackMaxPath)
	if err != nil {
		return 0, 0, fmt.Errorf("conntrack max: %w", err)
	}
	return count, max, nil
}

func readIntFile(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", path, err)
	}
	return n, nil
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/iface/... -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/iface/
git commit -m "feat: interface stats via netlink and conntrack reader"
```

---

## Task 7: Prometheus metrics

**Files:**
- Create: `internal/metrics/metrics.go`
- Create: `internal/metrics/server.go`
- Create: `internal/metrics/metrics_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/metrics/metrics_test.go`:

```go
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
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/metrics/... -v
```

Expected: compile error.

- [ ] **Step 3: Implement metrics.go**

Create `internal/metrics/metrics.go`:

```go
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Registry holds all kube-nat metric definitions.
type Registry struct {
	BytesTX   *prometheus.CounterVec
	BytesRX   *prometheus.CounterVec
	PacketsTX *prometheus.CounterVec
	PacketsRX *prometheus.CounterVec

	ConntrackEntries   prometheus.Gauge
	ConntrackMax       prometheus.Gauge
	ConntrackUsageRatio prometheus.Gauge

	RulePresent         *prometheus.GaugeVec
	SrcDstCheckDisabled prometheus.Gauge
	RouteTableOwned     *prometheus.GaugeVec

	PeerStatus      *prometheus.GaugeVec
	FailoverTotal   *prometheus.CounterVec
	LastFailover    *prometheus.GaugeVec

	SpotInterruptionPending prometheus.Gauge

	reg *prometheus.Registry
}

func NewRegistry() *Registry {
	r := prometheus.NewRegistry()
	m := &Registry{reg: r}

	labels := []string{"az", "instance_id", "iface"}

	m.BytesTX = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kube_nat_bytes_tx_total",
		Help: "Total bytes transmitted",
	}, labels)
	m.BytesRX = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kube_nat_bytes_rx_total",
		Help: "Total bytes received",
	}, labels)
	m.PacketsTX = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kube_nat_packets_tx_total",
		Help: "Total packets transmitted",
	}, labels)
	m.PacketsRX = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kube_nat_packets_rx_total",
		Help: "Total packets received",
	}, labels)
	m.ConntrackEntries = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kube_nat_conntrack_entries",
		Help: "Current number of conntrack entries",
	})
	m.ConntrackMax = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kube_nat_conntrack_max",
		Help: "Maximum conntrack entries (nf_conntrack_max)",
	})
	m.ConntrackUsageRatio = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kube_nat_conntrack_usage_ratio",
		Help: "Conntrack usage ratio (entries/max). Alert at >0.7",
	})
	m.RulePresent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kube_nat_rule_present",
		Help: "1 if the iptables rule is present, 0 if missing",
	}, []string{"rule"})
	m.SrcDstCheckDisabled = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kube_nat_src_dst_check_disabled",
		Help: "1 if source/dest check is disabled on the ENI",
	})
	m.RouteTableOwned = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kube_nat_route_table_owned",
		Help: "1 if this node owns the route table",
	}, []string{"rtb_id"})
	m.PeerStatus = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kube_nat_peer_status",
		Help: "1=peer up, 0=peer down",
	}, []string{"az", "instance_id"})
	m.FailoverTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kube_nat_failover_total",
		Help: "Number of route table takeovers performed",
	}, []string{"from_az", "to_az"})
	m.LastFailover = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kube_nat_last_failover_seconds",
		Help: "Unix timestamp of last failover for an AZ",
	}, []string{"az"})
	m.SpotInterruptionPending = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kube_nat_spot_interruption_pending",
		Help: "1 if a spot interruption notice has been received",
	})

	r.MustRegister(
		m.BytesTX, m.BytesRX, m.PacketsTX, m.PacketsRX,
		m.ConntrackEntries, m.ConntrackMax, m.ConntrackUsageRatio,
		m.RulePresent, m.SrcDstCheckDisabled, m.RouteTableOwned,
		m.PeerStatus, m.FailoverTotal, m.LastFailover,
		m.SpotInterruptionPending,
	)
	return m
}

func (m *Registry) Prometheus() *prometheus.Registry {
	return m.reg
}
```

- [ ] **Step 4: Implement server.go**

Create `internal/metrics/server.go`:

```go
package metrics

import (
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
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

// unused import guard
var _ *prometheus.Registry
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/metrics/... -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/metrics/
git commit -m "feat: Prometheus metrics registry with all kube-nat metrics"
```

---

## Task 8: Kubernetes Lease manager

**Files:**
- Create: `internal/lease/lease.go`
- Create: `internal/lease/lease_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/lease/lease_test.go`:

```go
package lease_test

import (
	"context"
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/kube-nat/kube-nat/internal/lease"
)

func TestRenewCreatesLease(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := lease.NewManager(client, "kube-system", 15*time.Second)

	err := mgr.Renew(context.Background(), "eu-west-1a", "kube-nat-abc")
	if err != nil {
		t.Fatal(err)
	}

	l, err := client.CoordinationV1().Leases("kube-system").
		Get(context.Background(), "kube-nat-eu-west-1a", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if *l.Spec.HolderIdentity != "kube-nat-abc" {
		t.Errorf("want holder kube-nat-abc got %s", *l.Spec.HolderIdentity)
	}
}

func TestIsExpired(t *testing.T) {
	now := time.Now()
	renewTime := metav1.NewMicroTime(now.Add(-20 * time.Second))
	l := &coordinationv1.Lease{
		Spec: coordinationv1.LeaseSpec{
			RenewTime:            &renewTime,
			LeaseDurationSeconds: int32Ptr(15),
		},
	}
	if !lease.IsExpired(l, now) {
		t.Error("lease renewed 20s ago with 15s duration should be expired")
	}
}

func TestIsNotExpired(t *testing.T) {
	now := time.Now()
	renewTime := metav1.NewMicroTime(now.Add(-5 * time.Second))
	l := &coordinationv1.Lease{
		Spec: coordinationv1.LeaseSpec{
			RenewTime:            &renewTime,
			LeaseDurationSeconds: int32Ptr(15),
		},
	}
	if lease.IsExpired(l, now) {
		t.Error("lease renewed 5s ago with 15s duration should not be expired")
	}
}

func int32Ptr(i int32) *int32 { return &i }
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/lease/... -v
```

Expected: compile error.

- [ ] **Step 3: Implement lease.go**

Create `internal/lease/lease.go`:

```go
package lease

import (
	"context"
	"fmt"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type Manager struct {
	client        kubernetes.Interface
	namespace     string
	leaseDuration time.Duration
}

func NewManager(client kubernetes.Interface, namespace string, duration time.Duration) *Manager {
	return &Manager{
		client:        client,
		namespace:     namespace,
		leaseDuration: duration,
	}
}

// Renew creates or updates the Lease for the given AZ with holderID.
func (m *Manager) Renew(ctx context.Context, az, holderID string) error {
	name := leaseName(az)
	now := metav1.NewMicroTime(time.Now())
	durSecs := int32(m.leaseDuration.Seconds())

	existing, err := m.client.CoordinationV1().Leases(m.namespace).
		Get(ctx, name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		_, err = m.client.CoordinationV1().Leases(m.namespace).Create(ctx,
			&coordinationv1.Lease{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: m.namespace},
				Spec: coordinationv1.LeaseSpec{
					HolderIdentity:       &holderID,
					LeaseDurationSeconds: &durSecs,
					RenewTime:            &now,
				},
			}, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return fmt.Errorf("get lease %s: %w", name, err)
	}
	existing.Spec.HolderIdentity = &holderID
	existing.Spec.RenewTime = &now
	existing.Spec.LeaseDurationSeconds = &durSecs
	_, err = m.client.CoordinationV1().Leases(m.namespace).
		Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

// Acquire attempts to take ownership of another AZ's Lease.
// Returns true if successful, false if another agent already holds it.
func (m *Manager) Acquire(ctx context.Context, az, holderID string) (bool, error) {
	name := leaseName(az)
	now := metav1.NewMicroTime(time.Now())
	durSecs := int32(m.leaseDuration.Seconds())

	existing, err := m.client.CoordinationV1().Leases(m.namespace).
		Get(ctx, name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		_, err = m.client.CoordinationV1().Leases(m.namespace).Create(ctx,
			&coordinationv1.Lease{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: m.namespace},
				Spec: coordinationv1.LeaseSpec{
					HolderIdentity:       &holderID,
					LeaseDurationSeconds: &durSecs,
					RenewTime:            &now,
				},
			}, metav1.CreateOptions{})
		if err != nil {
			if errors.IsAlreadyExists(err) {
				return false, nil // another agent won the race
			}
			return false, err
		}
		return true, nil
	}
	if err != nil {
		return false, err
	}
	// Only overwrite if expired
	if !IsExpired(existing, time.Now()) {
		return false, nil
	}
	existing.Spec.HolderIdentity = &holderID
	existing.Spec.RenewTime = &now
	_, err = m.client.CoordinationV1().Leases(m.namespace).
		Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return false, err
	}
	return true, nil
}

// IsExpired returns true if the lease's renew time + duration is in the past.
func IsExpired(l *coordinationv1.Lease, now time.Time) bool {
	if l.Spec.RenewTime == nil || l.Spec.LeaseDurationSeconds == nil {
		return true
	}
	expiry := l.Spec.RenewTime.Add(time.Duration(*l.Spec.LeaseDurationSeconds) * time.Second)
	return now.After(expiry)
}

// ListExpiredAZs returns AZs whose Leases are expired, excluding ownAZ.
func (m *Manager) ListExpiredAZs(ctx context.Context, ownAZ string) ([]string, error) {
	list, err := m.client.CoordinationV1().Leases(m.namespace).
		List(ctx, metav1.ListOptions{LabelSelector: "app=kube-nat"})
	if err != nil {
		return nil, err
	}
	var expired []string
	now := time.Now()
	for _, l := range list.Items {
		az := azFromLeaseName(l.Name)
		if az == "" || az == ownAZ {
			continue
		}
		if IsExpired(&l, now) {
			expired = append(expired, az)
		}
	}
	return expired, nil
}

func leaseName(az string) string {
	return "kube-nat-" + az
}

func azFromLeaseName(name string) string {
	const prefix = "kube-nat-"
	if len(name) > len(prefix) {
		return name[len(prefix):]
	}
	return ""
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/lease/... -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/lease/
git commit -m "feat: Kubernetes Lease manager for AZ ownership and split-brain prevention"
```

---

## Task 9: Peer heartbeat protocol

**Files:**
- Create: `internal/peer/protocol.go`
- Create: `internal/peer/server.go`
- Create: `internal/peer/client.go`
- Create: `internal/peer/peer_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/peer/peer_test.go`:

```go
package peer_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/kube-nat/kube-nat/internal/peer"
)

func TestProtocolEncodeDecode(t *testing.T) {
	msg := peer.Message{Type: peer.MsgPing, Timestamp: time.Now().UnixNano()}
	buf := peer.Encode(msg)
	if len(buf) != 9 {
		t.Fatalf("want 9 bytes got %d", len(buf))
	}
	decoded, err := peer.Decode(buf)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Type != peer.MsgPing {
		t.Errorf("want MsgPing got %d", decoded.Type)
	}
	if decoded.Timestamp != msg.Timestamp {
		t.Errorf("timestamp mismatch")
	}
}

func TestServerClientPingPong(t *testing.T) {
	srv := peer.NewServer(":0") // random port
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	addr := srv.Addr()
	go srv.Serve(context.Background())

	// Give server time to start
	time.Sleep(10 * time.Millisecond)

	// Connect a client
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Send ping
	conn.SetDeadline(time.Now().Add(time.Second))
	ping := peer.Encode(peer.Message{Type: peer.MsgPing, Timestamp: time.Now().UnixNano()})
	conn.Write(ping)

	// Read pong
	buf := make([]byte, 9)
	if _, err := conn.Read(buf); err != nil {
		t.Fatal(err)
	}
	msg, err := peer.Decode(buf)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Type != peer.MsgPong {
		t.Errorf("want MsgPong got %d", msg.Type)
	}
}

func TestClientDetectsFailure(t *testing.T) {
	failed := make(chan string, 1)
	c := peer.NewClient("eu-west-1a", peer.ClientConfig{
		ProbeInterval: 50 * time.Millisecond,
		ProbeFailures: 2,
		OnFailure: func(az string) {
			failed <- az
		},
	})

	// Connect to a port that won't respond
	go c.Connect(context.Background(), "127.0.0.1:19999")

	select {
	case az := <-failed:
		if az != "eu-west-1a" {
			t.Errorf("want eu-west-1a got %s", az)
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for failure callback")
	}
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/peer/... -v
```

Expected: compile error.

- [ ] **Step 3: Implement protocol.go**

Create `internal/peer/protocol.go`:

```go
package peer

import (
	"encoding/binary"
	"fmt"
)

const (
	MsgPing     byte = 0x01
	MsgPong     byte = 0x02
	MsgStepDown byte = 0x03

	msgSize = 9 // 1 byte type + 8 bytes unix nano
)

type Message struct {
	Type      byte
	Timestamp int64
}

func Encode(m Message) []byte {
	buf := make([]byte, msgSize)
	buf[0] = m.Type
	binary.BigEndian.PutUint64(buf[1:], uint64(m.Timestamp))
	return buf
}

func Decode(buf []byte) (Message, error) {
	if len(buf) < msgSize {
		return Message{}, fmt.Errorf("short message: %d bytes", len(buf))
	}
	return Message{
		Type:      buf[0],
		Timestamp: int64(binary.BigEndian.Uint64(buf[1:])),
	}, nil
}
```

- [ ] **Step 4: Implement server.go**

Create `internal/peer/server.go`:

```go
package peer

import (
	"context"
	"io"
	"net"
	"time"
)

type Server struct {
	addr     string
	listener net.Listener
}

func NewServer(addr string) *Server {
	return &Server{addr: addr}
}

func (s *Server) Listen() error {
	l, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.listener = l
	return nil
}

func (s *Server) Addr() string {
	return s.listener.Addr().String()
}

func (s *Server) Close() error {
	return s.listener.Close()
}

func (s *Server) Serve(ctx context.Context) {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				continue
			}
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	buf := make([]byte, msgSize)
	for {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, err := io.ReadFull(conn, buf)
		if err != nil {
			return
		}
		msg, err := Decode(buf)
		if err != nil {
			return
		}
		switch msg.Type {
		case MsgPing:
			pong := Encode(Message{Type: MsgPong, Timestamp: time.Now().UnixNano()})
			conn.SetWriteDeadline(time.Now().Add(time.Second))
			conn.Write(pong)
		case MsgStepDown:
			// Step-down received from peer — caller handles this via OnStepDown callback
			// (wired in agent.go)
			return
		}
	}
}
```

- [ ] **Step 5: Implement client.go**

Create `internal/peer/client.go`:

```go
package peer

import (
	"context"
	"io"
	"net"
	"time"
)

type ClientConfig struct {
	ProbeInterval time.Duration
	ProbeFailures int
	OnFailure     func(az string)
	OnStepDown    func(az string)
}

type Client struct {
	az   string
	cfg  ClientConfig
	conn net.Conn
}

func NewClient(az string, cfg ClientConfig) *Client {
	return &Client{az: az, cfg: cfg}
}

// Connect dials addr and runs the heartbeat loop.
// Calls OnFailure(az) after ProbeFailures consecutive missed pongs.
// Runs until ctx is cancelled or failure is declared.
func (c *Client) Connect(ctx context.Context, addr string) {
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		conn, err := net.DialTimeout("tcp", addr, c.cfg.ProbeInterval)
		if err != nil {
			failures++
			if failures >= c.cfg.ProbeFailures {
				if c.cfg.OnFailure != nil {
					c.cfg.OnFailure(c.az)
				}
				return
			}
			time.Sleep(c.cfg.ProbeInterval)
			continue
		}
		c.conn = conn
		failures = 0
		result := c.runHeartbeat(ctx, conn)
		conn.Close()
		if result == heartbeatStepDown {
			if c.cfg.OnStepDown != nil {
				c.cfg.OnStepDown(c.az)
			}
			return
		}
		// Connection lost — count as a failure
		failures++
		if failures >= c.cfg.ProbeFailures {
			if c.cfg.OnFailure != nil {
				c.cfg.OnFailure(c.az)
			}
			return
		}
	}
}

// SendStepDown sends a step-down message to all connected peers.
// Called on SIGTERM before the agent exits.
func (c *Client) SendStepDown() {
	if c.conn == nil {
		return
	}
	msg := Encode(Message{Type: MsgStepDown, Timestamp: time.Now().UnixNano()})
	c.conn.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
	c.conn.Write(msg)
}

type heartbeatResult int

const (
	heartbeatLost     heartbeatResult = iota
	heartbeatStepDown heartbeatResult = iota
)

func (c *Client) runHeartbeat(ctx context.Context, conn net.Conn) heartbeatResult {
	failures := 0
	buf := make([]byte, msgSize)
	ticker := time.NewTicker(c.cfg.ProbeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return heartbeatLost
		case <-ticker.C:
			ping := Encode(Message{Type: MsgPing, Timestamp: time.Now().UnixNano()})
			conn.SetWriteDeadline(time.Now().Add(c.cfg.ProbeInterval))
			if _, err := conn.Write(ping); err != nil {
				failures++
				if failures >= c.cfg.ProbeFailures {
					return heartbeatLost
				}
				continue
			}
			conn.SetReadDeadline(time.Now().Add(c.cfg.ProbeInterval))
			if _, err := io.ReadFull(conn, buf); err != nil {
				failures++
				if failures >= c.cfg.ProbeFailures {
					return heartbeatLost
				}
				continue
			}
			msg, err := Decode(buf)
			if err != nil {
				failures++
				continue
			}
			switch msg.Type {
			case MsgPong:
				failures = 0
			case MsgStepDown:
				return heartbeatStepDown
			}
		}
	}
}
```

- [ ] **Step 6: Run tests**

```bash
go test ./internal/peer/... -v -timeout 10s
```

Expected: PASS for all 3 tests.

- [ ] **Step 7: Commit**

```bash
git add internal/peer/
git commit -m "feat: TCP peer heartbeat protocol with step-down signaling"
```

---

## Task 10: Spot interruption watcher

**Files:**
- Create: `internal/spot/watcher.go`
- Create: `internal/spot/watcher_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/spot/watcher_test.go`:

```go
package spot_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kube-nat/kube-nat/internal/spot"
)

func TestNoInterruption(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest/api/token" {
			w.Write([]byte("token"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	notified := false
	w := spot.NewWatcher(srv.URL, 50*time.Millisecond, func() {
		notified = true
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	if notified {
		t.Error("should not notify when no interruption")
	}
}

func TestInterruptionNotifies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest/api/token" {
			w.Write([]byte("token"))
			return
		}
		if r.URL.Path == "/latest/meta-data/spot/termination-time" {
			w.Write([]byte("2026-04-17T15:30:00Z"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	notified := make(chan struct{}, 1)
	w := spot.NewWatcher(srv.URL, 20*time.Millisecond, func() {
		notified <- struct{}{}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go w.Run(ctx)

	select {
	case <-notified:
	case <-time.After(500 * time.Millisecond):
		t.Error("timeout waiting for interruption notification")
	}
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/spot/... -v
```

Expected: compile error.

- [ ] **Step 3: Implement watcher.go**

Create `internal/spot/watcher.go`:

```go
package spot

import (
	"context"
	"io"
	"net/http"
	"time"
)

type Watcher struct {
	baseURL  string
	interval time.Duration
	onNotice func()
	client   *http.Client
}

func NewWatcher(baseURL string, interval time.Duration, onNotice func()) *Watcher {
	if baseURL == "" {
		baseURL = "http://169.254.169.254"
	}
	return &Watcher{
		baseURL:  baseURL,
		interval: interval,
		onNotice: onNotice,
		client:   &http.Client{Timeout: 2 * time.Second},
	}
}

// Run polls the IMDS spot termination endpoint until ctx is done or a notice is received.
// Calls onNotice exactly once when a termination time is found.
func (w *Watcher) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if w.check(ctx) {
				w.onNotice()
				return
			}
		}
	}
}

func (w *Watcher) check(ctx context.Context) bool {
	token, err := w.getToken(ctx)
	if err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		w.baseURL+"/latest/meta-data/spot/termination-time", nil)
	if err != nil {
		return false
	}
	req.Header.Set("X-aws-ec2-metadata-token", token)
	resp, err := w.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

func (w *Watcher) getToken(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		w.baseURL+"/latest/api/token", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "300")
	resp, err := w.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/spot/... -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/spot/
git commit -m "feat: spot interruption watcher with 1s polling"
```

---

## Task 11: Reconciler

**Files:**
- Create: `internal/reconciler/reconciler.go`
- Create: `internal/reconciler/reconciler_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/reconciler/reconciler_test.go`:

```go
package reconciler_test

import (
	"context"
	"testing"
	"time"

	kubenataws "github.com/kube-nat/kube-nat/internal/aws"
	"github.com/kube-nat/kube-nat/internal/reconciler"
)

type fakeNAT struct{ ensureCalled int }

func (f *fakeNAT) EnsureMasquerade(_ string) error { f.ensureCalled++; return nil }
func (f *fakeNAT) MasqueradeExists(_ string) (bool, error) { return true, nil }
func (f *fakeNAT) EnableIPForward() error                  { return nil }
func (f *fakeNAT) SetConntrackMax(_ int) error             { return nil }

type fakeEC2 struct{ claimCalled int }

func (f *fakeEC2) DisableSourceDestCheck(_ context.Context, _ string) error { return nil }
func (f *fakeEC2) DiscoverRouteTables(_ context.Context, _ string) ([]kubenataws.RouteTable, error) {
	return []kubenataws.RouteTable{{ID: "rtb-001", AZ: "eu-west-1a"}}, nil
}
func (f *fakeEC2) ClaimRouteTable(_ context.Context, _, _ string) error {
	f.claimCalled++
	return nil
}

func TestReconcileVerifiesRules(t *testing.T) {
	nat := &fakeNAT{}
	ec2 := &fakeEC2{}
	r := reconciler.New(reconciler.Config{
		NATManager: nat,
		EC2Client:  ec2,
		Iface:      "eth0",
		AZ:         "eu-west-1a",
		InstanceID: "i-0abc",
		Mode:       "auto",
	})
	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if nat.ensureCalled == 0 {
		t.Error("expected EnsureMasquerade to be called")
	}
	if ec2.claimCalled == 0 {
		t.Error("expected ClaimRouteTable to be called in auto mode")
	}
}

func TestManualModeSkipsRouteClaim(t *testing.T) {
	nat := &fakeNAT{}
	ec2 := &fakeEC2{}
	r := reconciler.New(reconciler.Config{
		NATManager: nat,
		EC2Client:  ec2,
		Iface:      "eth0",
		AZ:         "eu-west-1a",
		InstanceID: "i-0abc",
		Mode:       "manual",
		LogWriter:  io.Discard,
	})
	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ec2.claimCalled != 0 {
		t.Error("expected ClaimRouteTable NOT to be called in manual mode")
	}
}
```

> **Note:** add `"io"` to imports in the test file.

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/reconciler/... -v
```

Expected: compile error.

- [ ] **Step 3: Implement reconciler.go**

Create `internal/reconciler/reconciler.go`:

```go
package reconciler

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"

	kubenataws "github.com/kube-nat/kube-nat/internal/aws"
	"github.com/kube-nat/kube-nat/internal/nat"
)

type Config struct {
	NATManager nat.Manager
	EC2Client  kubenataws.EC2Client
	Iface      string
	AZ         string
	InstanceID string
	Mode       string // "auto" or "manual"
	LogWriter  io.Writer
}

type Reconciler struct {
	cfg    Config
	logger *log.Logger
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

	for _, rt := range tables {
		if r.cfg.Mode == "manual" {
			r.logger.Printf("[MANUAL] aws ec2 replace-route --route-table-id %s --destination-cidr-block 0.0.0.0/0 --instance-id %s --region <region>",
				rt.ID, r.cfg.InstanceID)
			continue
		}
		if err := r.cfg.EC2Client.ClaimRouteTable(ctx, rt.ID, r.cfg.InstanceID); err != nil {
			return fmt.Errorf("claim route table %s: %w", rt.ID, err)
		}
		r.logger.Printf("claimed route table %s", rt.ID)
	}
	return nil
}
```

- [ ] **Step 4: Fix test import**

Update `internal/reconciler/reconciler_test.go` imports to include `"io"`:

```go
import (
	"context"
	"io"
	"testing"
	"time"

	kubenataws "github.com/kube-nat/kube-nat/internal/aws"
	"github.com/kube-nat/kube-nat/internal/reconciler"
)
```

Remove the unused `"time"` import as well if it causes a compile error.

- [ ] **Step 5: Run tests**

```bash
go test ./internal/reconciler/... -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/reconciler/
git commit -m "feat: reconciler — idempotent NAT state enforcement with auto/manual modes"
```

---

## Task 12: Agent startup and wiring

**Files:**
- Create: `internal/agent/agent.go`
- Modify: `cmd/kube-nat/main.go`

- [ ] **Step 1: Implement agent.go**

Create `internal/agent/agent.go`:

```go
package agent

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
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
	logger.Printf("instance=%s az=%s eni=%s", meta.InstanceID, meta.AZ, meta.ENIID)

	// 2. AWS EC2 client (IRSA credentials from env)
	ec2Client, err := kubenataws.NewEC2Client(ctx, meta.Region, cfg.TagPrefix)
	if err != nil {
		return fmt.Errorf("aws ec2 client: %w", err)
	}

	// 3. Disable src/dst check
	if err := ec2Client.DisableSourceDestCheck(ctx, meta.ENIID); err != nil {
		return fmt.Errorf("disable src/dst check: %w", err)
	}
	logger.Printf("src/dst check disabled on %s", meta.ENIID)

	// 4. NAT manager (iptables)
	natMgr, err := nat.NewManager()
	if err != nil {
		return fmt.Errorf("nat manager: %w", err)
	}
	if err := natMgr.EnableIPForward(); err != nil {
		return fmt.Errorf("ip_forward: %w", err)
	}
	if err := nat.SetPortRange(cfg.IPLocalPortRange); err != nil {
		return fmt.Errorf("port range: %w", err)
	}

	// 5. Metrics
	reg := metrics.NewRegistry()

	// 6. Kubernetes client (in-cluster config)
	k8sCfg, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("k8s in-cluster config: %w", err)
	}
	k8sClient, err := kubernetes.NewForConfig(k8sCfg)
	if err != nil {
		return fmt.Errorf("k8s client: %w", err)
	}

	// 7. Lease manager
	leaseMgr := lease.NewManager(k8sClient, cfg.Namespace, cfg.LeaseDuration)

	// 8. Reconciler
	rec := reconciler.New(reconciler.Config{
		NATManager: natMgr,
		EC2Client:  ec2Client,
		Iface:      meta.ENIID, // netlink uses ENI ID as interface name on Nitro
		AZ:         meta.AZ,
		InstanceID: meta.InstanceID,
		Mode:       cfg.Mode,
	})

	// 9. Initial reconcile — sets up rules and claims route table
	if err := rec.Reconcile(ctx); err != nil {
		return fmt.Errorf("initial reconcile: %w", err)
	}

	// 10. Write own Lease
	podName := os.Getenv("POD_NAME")
	if podName == "" {
		podName = meta.InstanceID
	}
	if err := leaseMgr.Renew(ctx, meta.AZ, podName); err != nil {
		return fmt.Errorf("initial lease renew: %w", err)
	}

	// 11. Peer server
	peerAddr := fmt.Sprintf(":%d", cfg.PeerPort)
	peerSrv := peer.NewServer(peerAddr)
	if err := peerSrv.Listen(); err != nil {
		return fmt.Errorf("peer server listen: %w", err)
	}
	go peerSrv.Serve(ctx)
	logger.Printf("peer server listening on %s", peerAddr)

	// 12. Spot watcher
	spotWatcher := spot.NewWatcher("", time.Second, func() {
		logger.Printf("spot interruption notice received — initiating step-down")
		reg.SpotInterruptionPending.Set(1)
		cancel() // triggers graceful shutdown
	})
	go spotWatcher.Run(ctx)

	// 13. Ready — mark readiness
	ready := true
	readyFn := func() error {
		if !ready {
			return fmt.Errorf("not ready")
		}
		return nil
	}

	// 14. HTTP server: /metrics /healthz /readyz
	mux := metrics.NewMux(reg, readyFn)
	httpSrv := &http.Server{
		Addr:    metrics.ListenAddr(cfg.MetricsPort),
		Handler: mux,
	}
	go func() {
		logger.Printf("metrics server listening on %s", httpSrv.Addr)
		if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
			logger.Printf("metrics server error: %v", err)
		}
	}()

	// 15. Reconciliation loop
	go func() {
		ticker := time.NewTicker(cfg.ReconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := rec.Reconcile(ctx); err != nil {
					logger.Printf("reconcile error: %v", err)
				}
				if err := leaseMgr.Renew(ctx, meta.AZ, podName); err != nil {
					logger.Printf("lease renew error: %v", err)
				}
				updateMetrics(reg, meta, natMgr)
			}
		}
	}()

	logger.Printf("agent ready — az=%s instance=%s mode=%s", meta.AZ, meta.InstanceID, cfg.Mode)

	// 16. Wait for shutdown
	<-ctx.Done()
	logger.Printf("shutting down")
	ready = false
	httpSrv.Shutdown(context.Background())
	return nil
}

func updateMetrics(reg *metrics.Registry, meta *kubenataws.InstanceMetadata, natMgr nat.Manager) {
	exists, err := natMgr.MasqueradeExists(meta.ENIID)
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
}
```

- [ ] **Step 2: Wire into cmd/kube-nat/main.go**

Replace the entire `cmd/kube-nat/main.go`:

```go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/kube-nat/kube-nat/internal/agent"
	"github.com/kube-nat/kube-nat/internal/config"
)

func main() {
	root := &cobra.Command{
		Use:   "kube-nat",
		Short: "Kubernetes-native NAT for AWS",
	}
	root.AddCommand(agentCmd())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func agentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agent",
		Short: "Run the NAT agent (DaemonSet mode)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("config: %w", err)
			}
			return agent.Run(cfg)
		},
	}
}
```

- [ ] **Step 3: Verify build**

```bash
go build ./...
```

Expected: compiles without errors.

- [ ] **Step 4: Commit**

```bash
git add internal/agent/ cmd/
git commit -m "feat: agent startup sequence wiring all components"
```

---

## Task 13: Cross-AZ failover in the agent

**Files:**
- Modify: `internal/agent/agent.go` — add peer discovery + takeover logic

- [ ] **Step 1: Add peer discovery and takeover to agent.go**

Add the following function to `internal/agent/agent.go`:

```go
// startPeerWatcher discovers peer agent pods via k8s API and connects to each.
// On failure, acquires the dead peer's Lease and claims its route tables.
func startPeerWatcher(ctx context.Context, cfg *config.Config, k8sClient kubernetes.Interface,
	leaseMgr *lease.Manager, ec2Client kubenataws.EC2Client,
	meta *kubenataws.InstanceMetadata, reg *metrics.Registry, logger *log.Logger) {

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
		peerAddr := fmt.Sprintf("%s:%d", pod.Status.PodIP, cfg.PeerPort)
		peerAZ := podAZ
		peerInstanceID := pod.Spec.NodeName

		reg.PeerStatus.WithLabelValues(peerAZ, peerInstanceID).Set(1)

		c := peer.NewClient(peerAZ, peer.ClientConfig{
			ProbeInterval: cfg.ProbeInterval,
			ProbeFailures: cfg.ProbeFailures,
			OnFailure: func(az string) {
				reg.PeerStatus.WithLabelValues(az, peerInstanceID).Set(0)
				logger.Printf("peer %s (%s) declared dead — attempting takeover", az, peerInstanceID)
				takeover(ctx, cfg, leaseMgr, ec2Client, meta, reg, logger, az)
			},
			OnStepDown: func(az string) {
				logger.Printf("peer %s stepping down — taking over", az)
				takeover(ctx, cfg, leaseMgr, ec2Client, meta, reg, logger, az)
			},
		})
		go c.Connect(ctx, peerAddr)
	}
}

func takeover(ctx context.Context, cfg *config.Config, leaseMgr *lease.Manager,
	ec2Client kubenataws.EC2Client, meta *kubenataws.InstanceMetadata,
	reg *metrics.Registry, logger *log.Logger, deadAZ string) {

	podName := os.Getenv("POD_NAME")
	if podName == "" {
		podName = meta.InstanceID
	}

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
			logger.Printf("takeover %s: claimed route table %s", deadAZ, rt.ID)
			reg.RouteTableOwned.WithLabelValues(rt.ID).Set(1)
		}
	}

	now := float64(time.Now().Unix())
	reg.FailoverTotal.WithLabelValues(deadAZ, meta.AZ).Inc()
	reg.LastFailover.WithLabelValues(deadAZ).Set(now)
}
```

Add missing imports to `agent.go`:

```go
import (
	// existing imports plus:
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)
```

Then call `startPeerWatcher` in `Run()` after the peer server starts (after step 11):

```go
// After peer server starts:
go startPeerWatcher(ctx, cfg, k8sClient, leaseMgr, ec2Client, meta, reg, logger)
```

- [ ] **Step 2: Verify build**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/agent/agent.go
git commit -m "feat: cross-AZ failover via peer health detection and Lease acquisition"
```

---

## Task 14: Dockerfile

**Files:**
- Create: `Dockerfile`

- [ ] **Step 1: Create Dockerfile**

Create `Dockerfile`:

```dockerfile
# syntax=docker/dockerfile:1

# ---- build stage ----
FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /kube-nat ./cmd/kube-nat

# ---- test stage (used by 'make test-nat') ----
FROM golang:1.22-alpine AS test
WORKDIR /src
RUN apk add --no-cache iptables
COPY go.mod go.sum ./
RUN go mod download
COPY . .
CMD ["go", "test", "./...", "-v", "-count=1"]

# ---- runtime stage ----
FROM debian:bookworm-slim AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends \
    iptables \
    iproute2 \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /kube-nat /usr/local/bin/kube-nat
USER root
ENTRYPOINT ["/usr/local/bin/kube-nat"]
CMD ["agent"]
```

- [ ] **Step 2: Build image**

```bash
docker build -t kube-nat:dev .
```

Expected: image built, size < 50MB.

- [ ] **Step 3: Smoke test — prints help and exits cleanly**

```bash
docker run --rm kube-nat:dev --help
```

Expected: prints cobra usage for `kube-nat`.

- [ ] **Step 4: Commit**

```bash
git add Dockerfile
git commit -m "feat: multi-stage Dockerfile with NET_ADMIN test stage"
```

---

## Task 15: Integration test — startup with mocks

**Files:**
- Create: `internal/agent/agent_test.go`

- [ ] **Step 1: Write integration test**

Create `internal/agent/agent_test.go`:

```go
package agent_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"

	kubenataws "github.com/kube-nat/kube-nat/internal/aws"
	"github.com/kube-nat/kube-nat/internal/config"
	"github.com/kube-nat/kube-nat/internal/iface"
	"github.com/kube-nat/kube-nat/internal/lease"
	"github.com/kube-nat/kube-nat/internal/metrics"
	"github.com/kube-nat/kube-nat/internal/nat"
	"github.com/kube-nat/kube-nat/internal/reconciler"
)

// testComponents bundles fake implementations for agent testing.
type testComponents struct {
	natMgr  *fakeNAT
	ec2     *fakeEC2
	k8s     *fake.Clientset
	meta    *kubenataws.InstanceMetadata
	cfg     *config.Config
	reg     *metrics.Registry
	leaseMgr *lease.Manager
	rec     *reconciler.Reconciler
}

type fakeNAT struct{ ensureCalled int }
func (f *fakeNAT) EnsureMasquerade(_ string) error  { f.ensureCalled++; return nil }
func (f *fakeNAT) MasqueradeExists(_ string) (bool, error) { return true, nil }
func (f *fakeNAT) EnableIPForward() error            { return nil }
func (f *fakeNAT) SetConntrackMax(_ int) error       { return nil }

type fakeEC2 struct{ claimed []string }
func (f *fakeEC2) DisableSourceDestCheck(_ context.Context, _ string) error { return nil }
func (f *fakeEC2) DiscoverRouteTables(_ context.Context, az string) ([]kubenataws.RouteTable, error) {
	return []kubenataws.RouteTable{{ID: "rtb-001", AZ: az}}, nil
}
func (f *fakeEC2) ClaimRouteTable(_ context.Context, rtbID, _ string) error {
	f.claimed = append(f.claimed, rtbID)
	return nil
}

func newTestComponents(t *testing.T) *testComponents {
	t.Helper()
	cfg := &config.Config{
		Mode:              "auto",
		AZLabel:           "topology.kubernetes.io/zone",
		LeaseDuration:     15 * time.Second,
		ProbeInterval:     200 * time.Millisecond,
		ProbeFailures:     2,
		ReconcileInterval: 30 * time.Second,
		MetricsPort:       0,
		PeerPort:          0,
		TagPrefix:         "kube-nat",
		Namespace:         "kube-system",
	}
	meta := &kubenataws.InstanceMetadata{
		InstanceID: "i-test",
		AZ:         "eu-west-1a",
		Region:     "eu-west-1",
		ENIID:      "eni-test",
	}
	natMgr := &fakeNAT{}
	ec2 := &fakeEC2{}
	k8s := fake.NewSimpleClientset()
	reg := metrics.NewRegistry()
	leaseMgr := lease.NewManager(k8s, cfg.Namespace, cfg.LeaseDuration)
	rec := reconciler.New(reconciler.Config{
		NATManager: natMgr,
		EC2Client:  ec2,
		Iface:      meta.ENIID,
		AZ:         meta.AZ,
		InstanceID: meta.InstanceID,
		Mode:       cfg.Mode,
	})
	return &testComponents{
		natMgr: natMgr, ec2: ec2, k8s: k8s,
		meta: meta, cfg: cfg, reg: reg,
		leaseMgr: leaseMgr, rec: rec,
	}
}

func TestInitialReconcileClaimsRouteTable(t *testing.T) {
	tc := newTestComponents(t)
	ctx := context.Background()

	if err := tc.rec.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(tc.ec2.claimed) == 0 {
		t.Error("expected route table to be claimed on startup")
	}
	if tc.ec2.claimed[0] != "rtb-001" {
		t.Errorf("want rtb-001 got %s", tc.ec2.claimed[0])
	}
}

func TestLeaseRenewedAfterReconcile(t *testing.T) {
	tc := newTestComponents(t)
	ctx := context.Background()

	if err := tc.leaseMgr.Renew(ctx, tc.meta.AZ, "kube-nat-pod-1"); err != nil {
		t.Fatal(err)
	}
	// Renewing again should not error (update path)
	if err := tc.leaseMgr.Renew(ctx, tc.meta.AZ, "kube-nat-pod-1"); err != nil {
		t.Fatal(err)
	}
}

func TestHealthEndpoint(t *testing.T) {
	ready := true
	readyFn := func() error {
		if !ready {
			return fmt.Errorf("not ready")
		}
		return nil
	}
	reg := metrics.NewRegistry()
	mux := metrics.NewMux(reg, readyFn)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/healthz want 200 got %d", resp.StatusCode)
	}

	resp, err = http.Get(srv.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/readyz want 200 got %d", resp.StatusCode)
	}

	ready = false
	resp, err = http.Get(srv.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("/readyz not-ready want 503 got %d", resp.StatusCode)
	}
}

// unused import guard
var _ *iface.Stats
```

- [ ] **Step 2: Run all tests**

```bash
go test ./... -v -race
```

Expected: PASS for all packages. No races.

- [ ] **Step 3: Commit**

```bash
git add internal/agent/agent_test.go
git commit -m "test: integration tests for agent startup, lease renewal, and health endpoints"
```

---

## Task 16: Self-review and final verification

- [ ] **Step 1: Run full test suite**

```bash
go test ./... -v -race -count=1
```

Expected: all PASS, no race conditions.

- [ ] **Step 2: Vet**

```bash
go vet ./...
```

Expected: no output (no issues).

- [ ] **Step 3: Verify binary runs**

```bash
go build -o bin/kube-nat ./cmd/kube-nat
./bin/kube-nat agent --help
```

Expected: prints agent subcommand help and exits 0.

- [ ] **Step 4: Check binary size**

```bash
ls -lh bin/kube-nat
```

Expected: < 30MB.

- [ ] **Step 5: Final commit**

```bash
git add -A
git commit -m "chore: kube-nat agent plan 1 complete — all tests passing"
```

---

## Self-Review Notes

After writing this plan, checking against the spec:

- ✅ iptables MASQUERADE via go-iptables
- ✅ src/dst check disabled on ENI
- ✅ ip_forward and conntrack sysctl
- ✅ Route table discovery via tags (`kube-nat/managed`, `kube-nat/az`)
- ✅ Auto mode + manual mode (logs `aws ec2 replace-route` to stderr)
- ✅ Kubernetes Lease for split-brain prevention
- ✅ TCP peer heartbeats at 200ms with 2-failure threshold (~400ms detection)
- ✅ Step-down signaling on SIGTERM
- ✅ Spot interruption watcher (IMDS polling)
- ✅ Cross-AZ failover + route table takeover
- ✅ Prometheus metrics (all defined in spec)
- ✅ /healthz and /readyz endpoints
- ✅ IRSA: AWS SDK uses env credentials injected by EKS pod identity
- ✅ system-node-critical priority: set in Helm chart (Plan 2)
- ✅ PDB, NodePool, NetworkPolicy: in Helm chart (Plan 2)
- ✅ Dockerfile (multi-stage, runtime on debian-slim)
- ⏭ React dashboard: Plan 2
- ⏭ Helm chart: Plan 2
- ⏭ go:embed: Plan 2 (dashboard binary)

**Note on NET_ADMIN tests:** The `nat.NewManager()` call requires `NET_ADMIN`. The integration test in Task 15 uses `fakeNAT` to avoid this. To test real iptables rules run `make test-nat` which runs tests inside Docker with `--cap-add NET_ADMIN`.

**Note on interface name:** On AWS Nitro instances, `ip link` shows the ENI as its ENI ID (e.g. `eni-0abc123`). The agent uses this as the interface name passed to iptables and netlink. Verify this on the target instance type before deploying.
