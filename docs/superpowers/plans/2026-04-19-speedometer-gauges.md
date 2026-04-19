# Speedometer Gauges Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add half-arc speedometer gauges for TX/RX bandwidth to each agent card and to the summary totals, where 100% equals the instance's peak network bandwidth queried from the AWS EC2 API.

**Architecture:** The agent fetches its instance type from IMDS at startup, calls `DescribeInstanceTypes` to get peak bandwidth in Gbps, exposes it as a Prometheus gauge, and the dashboard collector scrapes it alongside existing metrics. The frontend renders SVG half-arc gauges using this maximum as the scale.

**Tech Stack:** Go (aws-sdk-go-v2 ec2), Prometheus client_golang, React + TypeScript + Tailwind, SVG

---

## File Map

| File | Change |
|---|---|
| `internal/aws/metadata.go` | Add `InstanceType string` field, fetch from IMDS |
| `internal/aws/metadata_test.go` | Add `/latest/meta-data/instance-type` handler + assertion |
| `internal/aws/ec2.go` | Add `DescribeInstanceMaxBandwidth` to interface + implement |
| `internal/aws/ec2_test.go` | Add stub method to `fakeEC2` |
| `internal/agent/agent_test.go` | Add stub method to `fakeEC2` |
| `internal/metrics/metrics.go` | Add `MaxBandwidthBps prometheus.Gauge` |
| `internal/agent/agent.go` | Call `DescribeInstanceMaxBandwidth`, set gauge |
| `internal/collector/snapshot.go` | Add `MaxBandwidthBps float64` to `AgentSnap` |
| `internal/collector/collector.go` | Scrape `kube_nat_max_bandwidth_bps` in `buildSnap` |
| `web/src/types.ts` | Add `max_bw_bps: number` to `AgentSnap` |
| `web/src/components/SpeedometerGauge.tsx` | New component — SVG half-arc gauge |
| `web/src/components/AZCard.tsx` | Replace TX/RX text with two `SpeedometerGauge` |
| `web/src/components/SummaryCards.tsx` | Add `SpeedometerGauge` to TX and RX cards |
| `terraform/modules/infra/kubernetes/kube_nat/main.tf` | Add `ec2:DescribeInstanceTypes` to IAM |

---

## Task 1: Add InstanceType to InstanceMetadata

**Files:**
- Modify: `internal/aws/metadata.go`
- Modify: `internal/aws/metadata_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/aws/metadata_test.go`, add the IMDS handler and the assertion:

```go
// In newIMDSServer(), add before the return:
mux.HandleFunc("/latest/meta-data/instance-type", func(w http.ResponseWriter, r *http.Request) {
    w.Write([]byte("c6g.xlarge"))
})
```

In `TestFetchMetadata`, add:
```go
if meta.InstanceType != "c6g.xlarge" {
    t.Errorf("want c6g.xlarge got %s", meta.InstanceType)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /Users/michelfeldheim/Documents/Workspaces/infra/kube-nat
go test ./internal/aws/... -run TestFetchMetadata -v
```
Expected: FAIL — `meta.InstanceType` field does not exist

- [ ] **Step 3: Add InstanceType to the struct and fetch it**

In `internal/aws/metadata.go`, add `InstanceType string` to the struct:
```go
type InstanceMetadata struct {
    InstanceID   string
    AZ           string
    Region       string
    ENIID        string
    MAC          string
    PublicIface  string // Linux interface name, e.g. "eth0" (derived from device-number)
    InstanceType string // e.g. "c6g.xlarge"
}
```

In `Fetch()`, after the `deviceNum` fetch block and before the `return`, add:
```go
instanceType, err := get("/latest/meta-data/instance-type")
if err != nil {
    return nil, err
}
```

And include it in the returned struct:
```go
return &InstanceMetadata{
    InstanceID:   instanceID,
    AZ:           az,
    Region:       region,
    ENIID:        eniID,
    MAC:          mac,
    PublicIface:  iface,
    InstanceType: instanceType,
}, nil
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/aws/... -run TestFetchMetadata -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/michelfeldheim/Documents/Workspaces/infra/kube-nat
git add internal/aws/metadata.go internal/aws/metadata_test.go
git commit -m "feat(metadata): add InstanceType field fetched from IMDS"
```

---

## Task 2: Add DescribeInstanceMaxBandwidth to EC2Client

**Files:**
- Modify: `internal/aws/ec2.go`

- [ ] **Step 1: Add the interface method**

In `internal/aws/ec2.go`, add to the `EC2Client` interface:
```go
type EC2Client interface {
    DisableSourceDestCheck(ctx context.Context, eniID string) error
    DiscoverRouteTables(ctx context.Context, az string) ([]RouteTable, error)
    ClaimRouteTable(ctx context.Context, rtbID, instanceID string) error
    ReleaseRouteTable(ctx context.Context, rtbID, natGatewayID string) error
    LookupNatGateway(ctx context.Context, vpcID, az string) (string, error)
    DescribeInstanceMaxBandwidth(ctx context.Context, instanceType string) (float64, error)
}
```

- [ ] **Step 2: Add required imports**

The implementation needs `strconv` and `strings`. Add them to the import block in `ec2.go`:
```go
import (
    "context"
    "fmt"
    "strconv"
    "strings"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/ec2"
    "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)
```

- [ ] **Step 3: Implement on realEC2Client**

Append to the bottom of `internal/aws/ec2.go`:

```go
// DescribeInstanceMaxBandwidth returns the peak network bandwidth in bytes/s for
// the given instance type. Uses PeakBandwidthInGbps when available (modern instance
// types); falls back to parsing the NetworkPerformance string for older types.
func (c *realEC2Client) DescribeInstanceMaxBandwidth(ctx context.Context, instanceType string) (float64, error) {
    out, err := c.svc.DescribeInstanceTypes(ctx, &ec2.DescribeInstanceTypesInput{
        InstanceTypes: []types.InstanceType{types.InstanceType(instanceType)},
    })
    if err != nil {
        return 0, fmt.Errorf("describe instance types %s: %w", instanceType, err)
    }
    if len(out.InstanceTypes) == 0 {
        return 0, fmt.Errorf("instance type %s not found", instanceType)
    }
    ni := out.InstanceTypes[0].NetworkInfo
    if ni == nil {
        return 0, fmt.Errorf("no network info for instance type %s", instanceType)
    }
    if ni.NetworkBandwidth != nil && ni.NetworkBandwidth.PeakBandwidthInGbps != nil {
        return *ni.NetworkBandwidth.PeakBandwidthInGbps * 1e9, nil
    }
    if ni.NetworkPerformance != nil {
        return parseNetworkPerformanceBps(*ni.NetworkPerformance)
    }
    return 0, fmt.Errorf("cannot determine max bandwidth for instance type %s", instanceType)
}

// parseNetworkPerformanceBps parses a NetworkPerformance string like "25 Gbps",
// "Up to 25 Gbps", or "10 Gbps" into bytes per second.
func parseNetworkPerformanceBps(s string) (float64, error) {
    s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "up to ")
    parts := strings.Fields(s)
    if len(parts) < 2 {
        return 0, fmt.Errorf("cannot parse network performance %q", s)
    }
    val, err := strconv.ParseFloat(parts[0], 64)
    if err != nil {
        return 0, fmt.Errorf("cannot parse network performance %q: %w", s, err)
    }
    switch strings.ToLower(parts[1]) {
    case "gbps":
        return val * 1e9, nil
    case "mbps":
        return val * 1e6, nil
    default:
        return 0, fmt.Errorf("unknown unit in network performance %q", s)
    }
}
```

- [ ] **Step 4: Verify it compiles**

```bash
cd /Users/michelfeldheim/Documents/Workspaces/infra/kube-nat
go build ./internal/aws/...
```
Expected: no errors

- [ ] **Step 5: Commit**

```bash
git add internal/aws/ec2.go
git commit -m "feat(ec2): add DescribeInstanceMaxBandwidth via DescribeInstanceTypes API"
```

---

## Task 3: Update fakeEC2 stubs in test files

**Files:**
- Modify: `internal/aws/ec2_test.go`
- Modify: `internal/agent/agent_test.go`

- [ ] **Step 1: Add stub to ec2_test.go**

In `internal/aws/ec2_test.go`, add to `fakeEC2`:
```go
func (f *fakeEC2) DescribeInstanceMaxBandwidth(_ context.Context, _ string) (float64, error) {
    return 25e9, nil // 25 Gbps
}
```

- [ ] **Step 2: Add stub to agent_test.go**

In `internal/agent/agent_test.go`, add to `fakeEC2`:
```go
func (f *fakeEC2) DescribeInstanceMaxBandwidth(_ context.Context, _ string) (float64, error) {
    return 25e9, nil // 25 Gbps
}
```

- [ ] **Step 3: Run all tests to verify they compile and pass**

```bash
cd /Users/michelfeldheim/Documents/Workspaces/infra/kube-nat
go test ./...
```
Expected: all PASS (or pre-existing failures only — no new failures)

- [ ] **Step 4: Commit**

```bash
git add internal/aws/ec2_test.go internal/agent/agent_test.go
git commit -m "test: add DescribeInstanceMaxBandwidth stub to fakeEC2 in test files"
```

---

## Task 4: Add MaxBandwidthBps Prometheus gauge

**Files:**
- Modify: `internal/metrics/metrics.go`

- [ ] **Step 1: Add the field and registration**

In `internal/metrics/metrics.go`, add `MaxBandwidthBps prometheus.Gauge` to the `Registry` struct after `SpotInterruptionPending`:
```go
SpotInterruptionPending prometheus.Gauge
MaxBandwidthBps         prometheus.Gauge
```

In `NewRegistry()`, add after the `SpotInterruptionPending` definition:
```go
m.MaxBandwidthBps = prometheus.NewGauge(prometheus.GaugeOpts{
    Name: "kube_nat_max_bandwidth_bps",
    Help: "Peak network bandwidth of this instance in bytes/s (from EC2 DescribeInstanceTypes)",
})
```

Add it to `r.MustRegister(...)`:
```go
r.MustRegister(
    m.BytesTX, m.BytesRX, m.PacketsTX, m.PacketsRX,
    m.ConntrackEntries, m.ConntrackMax, m.ConntrackUsageRatio,
    m.RulePresent, m.SrcDstCheckDisabled, m.RouteTableOwned,
    m.PeerStatus, m.FailoverTotal, m.LastFailover,
    m.SpotInterruptionPending, m.MaxBandwidthBps,
)
```

- [ ] **Step 2: Verify it compiles**

```bash
cd /Users/michelfeldheim/Documents/Workspaces/infra/kube-nat
go build ./internal/metrics/...
```
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/metrics/metrics.go
git commit -m "feat(metrics): add kube_nat_max_bandwidth_bps gauge"
```

---

## Task 5: Set the gauge at agent startup

**Files:**
- Modify: `internal/agent/agent.go`

- [ ] **Step 1: Call DescribeInstanceMaxBandwidth and set the gauge**

In `internal/agent/agent.go`, after step 5 (metrics initialization block where `reg.SrcDstCheckDisabled.Set(1)` is called), add:

```go
// Fetch and expose peak network bandwidth for this instance type.
if maxBps, err := ec2Client.DescribeInstanceMaxBandwidth(ctx, meta.InstanceType); err != nil {
    logger.Printf("WARNING: could not determine max bandwidth for %s: %v", meta.InstanceType, err)
} else {
    reg.MaxBandwidthBps.Set(maxBps)
    logger.Printf("instance type=%s peak bandwidth=%.0f bps (%.1f Gbps)", meta.InstanceType, maxBps, maxBps/1e9)
}
```

This must go after both the EC2 client init (step 2) and the metrics registry init (step 5). In the current code that is after line 81 (`reg.PacketsRX.WithLabelValues(...)`) and before the Kubernetes client init.

- [ ] **Step 2: Verify it compiles**

```bash
cd /Users/michelfeldheim/Documents/Workspaces/infra/kube-nat
go build ./internal/agent/...
```
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/agent/agent.go
git commit -m "feat(agent): set kube_nat_max_bandwidth_bps from EC2 DescribeInstanceTypes at startup"
```

---

## Task 6: Propagate MaxBandwidthBps through collector to AgentSnap

**Files:**
- Modify: `internal/collector/snapshot.go`
- Modify: `internal/collector/collector.go`

- [ ] **Step 1: Add field to AgentSnap**

In `internal/collector/snapshot.go`, add to the `AgentSnap` struct after `LastFailoverTS`:
```go
LastFailoverTS   float64  `json:"last_failover_ts"` // unix seconds, 0 if never
MaxBandwidthBps  float64  `json:"max_bw_bps"`       // peak network bandwidth in bytes/s
```

- [ ] **Step 2: Scrape the metric in buildSnap**

In `internal/collector/collector.go`, in `buildSnap()`, add after the `snap.SpotPending` line:
```go
snap.MaxBandwidthBps = gaugeVal(families, "kube_nat_max_bandwidth_bps")
```

- [ ] **Step 3: Verify it compiles**

```bash
cd /Users/michelfeldheim/Documents/Workspaces/infra/kube-nat
go build ./internal/collector/...
```
Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add internal/collector/snapshot.go internal/collector/collector.go
git commit -m "feat(collector): propagate kube_nat_max_bandwidth_bps into AgentSnap"
```

---

## Task 7: Add max_bw_bps to TypeScript types

**Files:**
- Modify: `web/src/types.ts`

- [ ] **Step 1: Add the field**

In `web/src/types.ts`, add `max_bw_bps` to the `AgentSnap` interface after `last_failover_ts`:
```ts
export interface AgentSnap {
  az: string
  instance_id: string
  tx_bps: number
  rx_bps: number
  conntrack_entries: number
  conntrack_max: number
  conntrack_ratio: number
  route_tables: string[]
  peer_up: boolean
  spot_pending: boolean
  rule_present: boolean
  src_dst_disabled: boolean
  last_failover_ts: number  // unix seconds, 0 if never
  max_bw_bps: number        // peak network bandwidth in bytes/s
}
```

- [ ] **Step 2: Commit**

```bash
cd /Users/michelfeldheim/Documents/Workspaces/infra/kube-nat
git add web/src/types.ts
git commit -m "feat(types): add max_bw_bps to AgentSnap"
```

---

## Task 8: Create SpeedometerGauge component

**Files:**
- Create: `web/src/components/SpeedometerGauge.tsx`

- [ ] **Step 1: Create the component**

Create `web/src/components/SpeedometerGauge.tsx`:

```tsx
interface Props {
  value: number
  max: number
  color: string  // e.g. "#34d399" or "#60a5fa"
  label: string  // "TX" or "RX"
}

function fmtBps(bps: number): string {
  if (bps >= 1e9) return `${(bps / 1e9).toFixed(1)} GB/s`
  if (bps >= 1e6) return `${(bps / 1e6).toFixed(1)} MB/s`
  if (bps >= 1e3) return `${(bps / 1e3).toFixed(1)} KB/s`
  return `${bps.toFixed(0)} B/s`
}

// Half-arc (semicircle) SVG speedometer gauge.
// Arc: M8,38 A28,28 0 0,1 64,38 — centre (36,38), radius 28, sweeps upward left→right.
// Arc length = π * 28 ≈ 87.96
export function SpeedometerGauge({ value, max, color, label }: Props) {
  const pct = max > 0 ? Math.min(value / max, 1) : 0
  const arcLen = Math.PI * 28
  const filled = pct * arcLen
  const gap = arcLen - filled

  return (
    <div className="flex flex-col items-center">
      <svg width="72" height="44" viewBox="0 0 72 44" aria-label={`${label} ${(pct * 100).toFixed(0)}%`}>
        {/* track */}
        <path
          d="M8,38 A28,28 0 0,1 64,38"
          fill="none"
          stroke="#1f2937"
          strokeWidth="5"
          strokeLinecap="round"
        />
        {/* fill */}
        <path
          d="M8,38 A28,28 0 0,1 64,38"
          fill="none"
          stroke={color}
          strokeWidth="5"
          strokeLinecap="round"
          strokeDasharray={`${filled.toFixed(2)} ${gap.toFixed(2)}`}
        />
        {/* percentage */}
        <text
          x="36"
          y="31"
          textAnchor="middle"
          fontSize="10"
          fill="#9ca3af"
          fontFamily="monospace"
        >
          {(pct * 100).toFixed(0)}%
        </text>
      </svg>
      <div className="text-xs text-gray-400 -mt-1">{label}</div>
      <div className="text-xs text-gray-100 font-bold">{fmtBps(value)}</div>
    </div>
  )
}
```

- [ ] **Step 2: Commit**

```bash
cd /Users/michelfeldheim/Documents/Workspaces/infra/kube-nat
git add web/src/components/SpeedometerGauge.tsx
git commit -m "feat(ui): add SpeedometerGauge half-arc SVG component"
```

---

## Task 9: Use SpeedometerGauge in AZCard

**Files:**
- Modify: `web/src/components/AZCard.tsx`

- [ ] **Step 1: Replace TX/RX text row with gauges**

In `web/src/components/AZCard.tsx`:

1. Add the import at the top of the file (after the existing `import { useState } from 'react'`):
```tsx
import { SpeedometerGauge } from './SpeedometerGauge'
```

2. Replace the existing TX/RX text block (lines 61–64):
```tsx
<div className="flex gap-4 text-sm">
  <div><span className="text-gray-400">TX </span><span>{fmtBps(a.tx_bps)}</span></div>
  <div><span className="text-gray-400">RX </span><span>{fmtBps(a.rx_bps)}</span></div>
</div>
```
with:
```tsx
<div className="flex gap-3 justify-center">
  <SpeedometerGauge value={a.tx_bps} max={a.max_bw_bps ?? 0} color="#34d399" label="TX" />
  <SpeedometerGauge value={a.rx_bps} max={a.max_bw_bps ?? 0} color="#60a5fa" label="RX" />
</div>
```

3. Remove the now-unused `fmtBps` function from `AZCard.tsx` (lines 8–12):
```tsx
function fmtBps(bps: number): string {
  if (bps >= 1e6) return `${(bps / 1e6).toFixed(1)} MB/s`
  if (bps >= 1e3) return `${(bps / 1e3).toFixed(1)} KB/s`
  return `${bps.toFixed(0)} B/s`
}
```

- [ ] **Step 2: Verify TypeScript compiles**

```bash
cd /Users/michelfeldheim/Documents/Workspaces/infra/kube-nat/web
npm run build 2>&1 | tail -20
```
Expected: no TypeScript errors

- [ ] **Step 3: Commit**

```bash
cd /Users/michelfeldheim/Documents/Workspaces/infra/kube-nat
git add web/src/components/AZCard.tsx
git commit -m "feat(ui): replace TX/RX text with SpeedometerGauge in AZCard"
```

---

## Task 10: Use SpeedometerGauge in SummaryCards

**Files:**
- Modify: `web/src/components/SummaryCards.tsx`

- [ ] **Step 1: Add gauge to TX and RX summary cards**

In `web/src/components/SummaryCards.tsx`:

1. Add the import after the existing imports:
```tsx
import { SpeedometerGauge } from './SpeedometerGauge'
```

2. Add `totalMaxBw` to the computed values block (after `const fo24h = ...`):
```tsx
const totalMaxBw = agents.reduce((s, a) => s + (a.max_bw_bps ?? 0), 0)
```

3. Replace the TX and RX `<Card>` elements:

Replace:
```tsx
<Card label="TX" value={fmtBps(totalTx)} />
<Card label="RX" value={fmtBps(totalRx)} />
```
With:
```tsx
<Card label="TX" value={fmtBps(totalTx)}>
  <SpeedometerGauge value={totalTx} max={totalMaxBw} color="#34d399" label="" />
</Card>
<Card label="RX" value={fmtBps(totalRx)}>
  <SpeedometerGauge value={totalRx} max={totalMaxBw} color="#60a5fa" label="" />
</Card>
```

- [ ] **Step 2: Verify TypeScript compiles**

```bash
cd /Users/michelfeldheim/Documents/Workspaces/infra/kube-nat/web
npm run build 2>&1 | tail -20
```
Expected: no TypeScript errors

- [ ] **Step 3: Commit**

```bash
cd /Users/michelfeldheim/Documents/Workspaces/infra/kube-nat
git add web/src/components/SummaryCards.tsx
git commit -m "feat(ui): add SpeedometerGauge to TX/RX summary cards"
```

---

## Task 11: Add ec2:DescribeInstanceTypes to IAM policy

**Files:**
- Modify: `terraform/modules/infra/kubernetes/kube_nat/main.tf`

- [ ] **Step 1: Add the permission**

In `terraform/modules/infra/kubernetes/kube_nat/main.tf`, in the `DescribeEC2ForNAT` statement, add `"ec2:DescribeInstanceTypes"`:

```hcl
{
  Sid    = "DescribeEC2ForNAT"
  Effect = "Allow"
  Action = [
    "ec2:DescribeAddresses",
    "ec2:DescribeInstances",
    "ec2:DescribeInstanceTypes",
    "ec2:DescribeRouteTables",
    "ec2:DescribeNatGateways",
    "ec2:DescribeNetworkInterfaces",
    "ec2:DescribeSubnets",
  ]
  Resource = "*"
},
```

- [ ] **Step 2: Apply the change**

```bash
cd /Users/michelfeldheim/Github/terraform-infra/terraform
terraform apply -target='module.kube_nat' -auto-approve
```
Expected: 1 change — IAM policy updated

- [ ] **Step 3: Commit terraform**

```bash
cd /Users/michelfeldheim/Github/terraform-infra/terraform
git add modules/infra/kubernetes/kube_nat/main.tf
git commit -m "feat(iam): add ec2:DescribeInstanceTypes to KubeNat IAM policy"
```

---

## Task 12: Build, tag, and deploy

- [ ] **Step 1: Run all Go tests**

```bash
cd /Users/michelfeldheim/Documents/Workspaces/infra/kube-nat
go test ./...
```
Expected: all PASS

- [ ] **Step 2: Build frontend**

```bash
cd /Users/michelfeldheim/Documents/Workspaces/infra/kube-nat/web
npm run build
```
Expected: no errors, `dist/` updated

- [ ] **Step 3: Determine next version**

Current version is `v0.1.6`. This is a feature addition → bump to `v0.1.7`.

Update `deploy/helm/Chart.yaml`:
```yaml
version: 0.1.7
appVersion: "0.1.7"
```

- [ ] **Step 4: Commit version bump**

```bash
cd /Users/michelfeldheim/Documents/Workspaces/infra/kube-nat
git add deploy/helm/Chart.yaml
git commit -m "chore(helm): bump to chart 0.1.7 / image v0.1.7"
```

- [ ] **Step 5: Tag and push**

```bash
cd /Users/michelfeldheim/Documents/Workspaces/infra/kube-nat
git tag v0.1.7
git push origin main --tags
```

- [ ] **Step 6: Update ArgoCD to v0.1.7**

In `/Users/michelfeldheim/Github/terraform-infra/argocd/infrastructure/kubernetes/kube-nat/kustomization.yaml`, update version `0.1.6` → `0.1.7`.

In `/Users/michelfeldheim/Github/terraform-infra/argocd/infrastructure/kubernetes/kube-nat/values.yaml`, update `image.tag: v0.1.6` → `v0.1.7`.

- [ ] **Step 7: Commit and push ArgoCD update**

```bash
cd /Users/michelfeldheim/Github/terraform-infra
git add argocd/infrastructure/kubernetes/kube-nat/
git commit -m "chore(kube-nat): bump to chart 0.1.7 / image v0.1.7"
git push
```
