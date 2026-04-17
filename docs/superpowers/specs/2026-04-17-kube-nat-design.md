# kube-nat Design Spec

**Date:** 2026-04-17  
**Status:** Approved

## Overview

kube-nat is a Kubernetes-native AWS NAT replacement written in Go. It runs as a DaemonSet on labeled public-subnet EC2 nodes, manages iptables MASQUERADE rules and AWS route tables, and provides cross-AZ failover with near-zero blackhole time. It replaces AWS Managed NAT Gateway at a fraction of the cost. Designed for 100% spot instance fleets managed by Karpenter.

---

## Architecture

### Binary Modes

Single Docker image, single Go binary, two subcommands:

**`kube-nat agent`** — DaemonSet on labeled nodes  
- Configures iptables MASQUERADE via `coreos/go-iptables`
- Reads interface stats via `vishvananda/netlink` (bandwidth metrics at zero overhead)
- Disables src/dst check on ENI via AWS EC2 API
- Manages AZ route tables automatically, or logs `aws ec2 replace-route ...` instructions in manual mode
- Writes Kubernetes Lease heartbeat for own AZ
- Maintains persistent TCP peer connections to all other agents for failure detection
- Watches spot interruption notices via EC2 metadata
- Exposes `/metrics` (Prometheus) on `:9100` and peer port on `:9101`
- Exposes `/healthz` and `/readyz`

**`kube-nat dashboard`** — Deployment (1 replica)  
- Discovers all agent pods via Kubernetes API (list/watch by label)
- Scrapes `/metrics` from each agent on a 5s interval
- Aggregates bandwidth, conntrack, failover, and health data
- Serves React SPA on `:8080` with real-time WebSocket updates to browser
- No AWS API access required

### Package Layout

```
kube-nat/
├── cmd/
│   ├── agent/          # agent subcommand entrypoint
│   └── dashboard/      # dashboard subcommand entrypoint
├── internal/
│   ├── nat/            # iptables rule management via go-iptables
│   ├── iface/          # interface discovery + bandwidth stats via netlink
│   ├── aws/            # EC2 metadata, ENI src/dst check, route table management
│   ├── lease/          # Kubernetes Lease heartbeat + watcher
│   ├── peer/           # TCP peer connections, keepalives, step-down signaling
│   ├── reconciler/     # main control loop — desired vs actual state
│   ├── metrics/        # Prometheus collectors
│   └── collector/      # dashboard: scrapes agents, aggregates metrics
├── web/                # React SPA (embedded via go:embed)
├── deploy/
│   ├── manifests/      # raw Kubernetes YAML
│   └── helm/           # Helm chart
└── Dockerfile
```

---

## Topology

- **Multi-AZ**: one kube-nat agent per AZ (minimum), running on nodes labeled `node-role.kubernetes.io/nat=true`
- **Node placement**: explicit label — applied manually to dedicated NAT nodes or Karpenter node pools
- **AWS auth**: IRSA (IAM Roles for Service Accounts) — IAM role bound to ServiceAccount via annotation; AWS SDK auto-discovers credentials from pod environment

---

## Route Table Management

### Route table discovery convention
kube-nat discovers route tables by tag. Route tables must be tagged:
```
kube-nat/managed = "true"
kube-nat/az      = "us-east-1a"   # matches topology.kubernetes.io/zone
```
Tag key prefix is configurable via `KUBE_NAT_TAG_PREFIX` (default: `kube-nat`).

### Auto mode (default)
On startup, agent:
1. Queries EC2 metadata to determine own AZ and instance ID
2. Discovers all private subnet route tables tagged `kube-nat/az=<own-az>` via AWS API
3. Upserts `0.0.0.0/0 → this instance` in each route table

### Manual mode (`KUBE_NAT_MODE=manual`)
Route table writes are skipped entirely. Instead, the agent logs human-readable instructions to stderr:
```
[MANUAL] aws ec2 replace-route --route-table-id rtb-0abc123 --destination-cidr-block 0.0.0.0/0 --instance-id i-0abc123 --region us-east-1
```
All other functions (iptables, src/dst check, metrics, failover detection) operate normally.

---

## Failover

### Design principle
iptables rules persist on the host independent of the pod lifecycle. A pod restart on the same node is a non-event — NAT continues uninterrupted. Failover is only needed when the **node itself dies** and its route table entry must be transferred to another node.

### Three-layer failover

**Layer 1 — Spot interruption (proactive, ~100ms)**  
Agent polls `http://169.254.169.254/latest/meta-data/spot/termination-time` every 1 second. On notice, immediately sends step-down signal to peers before Kubernetes reacts. Covers the majority of spot replacements (AWS guarantees 2-minute warning).

**Layer 2 — Graceful shutdown (~100ms)**  
On SIGTERM, agent sends step-down message over TCP peer connection before exiting. Receiving peer immediately acquires Lease for the departing AZ and updates route tables. Covers Karpenter consolidation and rolling updates. `PodDisruptionBudget` with `maxUnavailable: 0` forces Karpenter to drain gracefully, ensuring this path is always taken for planned replacements.

**Layer 3 — Hard failure detection (~400ms)**  
Each agent maintains persistent TCP connections to all peer agents on port `:9101`. Detection uses **application-level heartbeats** (not TCP keepalives — Linux `TCP_KEEPIDLE` minimum is 1 second, too slow):
- Each agent sends a binary ping message over the connection every **200ms**
- Peer must respond with pong within 200ms
- **2 consecutive missed pongs** (400ms total) → peer declared dead → trigger AZ takeover
- TCP keepalives (`TCP_KEEPIDLE=1s`) set as a secondary backstop for silent connection drops

Covers unexpected node failures (kernel panic, hardware failure, network partition).

### Split-brain prevention
Kubernetes Lease used as a distributed lock, not a timer. Only the agent that successfully acquires the Lease for a given AZ writes that AZ's route tables. Prevents two agents from racing to update the same route table.

### Takeover election
When multiple agents detect the same peer failure simultaneously, the agent with the lexicographically lowest pod name wins the Lease acquisition race. Deterministic, no special coordination required.

### Recovery
When a failed node's replacement comes online, it runs the startup sequence, detects that its AZ's route tables point to a foreign instance, reclaims them, and writes a fresh Lease. Former covering agent sees the healthy Lease and stops covering.

### Failover time summary

| Scenario | Blackhole window |
|---|---|
| Karpenter drain / rolling update | ~100ms |
| Spot interruption (2-min notice) | ~100ms |
| Hard node failure (kernel panic, hardware) | ~400ms |

---

## Agent Startup Sequence

1. Query EC2 metadata → instance ID, AZ, ENI ID, public interface name
2. Call AWS EC2 API → disable source/dest check on ENI
3. Configure iptables MASQUERADE (idempotent, via go-iptables)
4. Enable `net.ipv4.ip_forward` via sysctl
5. Discover private subnet route tables for this AZ
6a. **Auto mode**: upsert `0.0.0.0/0 → self` in each route table  
6b. **Manual mode**: log `aws ec2 replace-route ...` to stderr
7. Write Kubernetes Lease for own AZ
8. Discover peer agent pods via Kubernetes API, open TCP connections to each
9. Start reconciliation loop (30s), metrics server (`:9100`), peer server (`:9101`)
10. Start spot interruption watcher (1s poll)
11. Mark `/readyz` healthy → pod becomes Ready

---

## Reconciliation Loop (every 30s)

- Re-verify iptables MASQUERADE rule exists (re-add if missing — e.g. after node-level iptables flush)
- Re-verify src/dst check is still disabled on ENI
- Re-verify owned route tables still point to this instance (correct manual drift)
- Check Leases for any expired AZs not yet covered (last-resort catch-all)
- Collect netlink interface counters → update Prometheus metrics
- Renew own Lease TTL

---

## Metrics

All metrics exposed on agent `:9100/metrics` in Prometheus format.

### Bandwidth
```
kube_nat_bytes_tx_total{az, instance_id, iface}
kube_nat_bytes_rx_total{az, instance_id, iface}
kube_nat_packets_tx_total{az, instance_id, iface}
kube_nat_packets_rx_total{az, instance_id, iface}
```

### Connection tracking
```
kube_nat_conntrack_entries           # current tracked connections
kube_nat_conntrack_max               # nf_conntrack_max kernel limit
kube_nat_conntrack_usage_ratio       # entries/max — alert threshold: >0.8
```

### NAT health
```
kube_nat_rule_present{rule}          # 1 if iptables rule exists, 0 if missing
kube_nat_src_dst_check_disabled      # 1 = OK, 0 = alert
kube_nat_route_table_owned{rtb_id}   # 1 if this node owns it
```

### Peer health & failover
```
kube_nat_peer_status{az, instance_id}        # 1=up, 0=down
kube_nat_failover_total{from_az, to_az}      # counter of takeovers performed
kube_nat_last_failover_seconds{az}           # unix timestamp of last takeover
```

### Spot
```
kube_nat_spot_interruption_pending   # 1 if interruption notice received
```

---

## Dashboard

React SPA embedded in binary via `//go:embed web/dist`. Served by `kube-nat dashboard` on `:8080`.

### Features
- Global header: cluster name, AZ count, node count, overall health status
- Summary cards: total throughput (TX/RX), active connections (with conntrack % bar), failover count (24h)
- Per-AZ cards: instance ID, instance type, TX/RX Mbps, active connections, owned route tables, spot interruption countdown when notice received
- Bandwidth sparkline: all AZs combined, last 5 minutes, updated every 5s via WebSocket
- Failover event log: timestamp, from-AZ, to-AZ, reason, duration

### Access
Dashboard is accessed via `kubectl port-forward` — it is not exposed via Ingress or LoadBalancer by default. A ClusterIP Service is included so teams can wire up their own ingress if needed.

### Data flow
Dashboard pod → k8s API (list/watch pods with `app=kube-nat`) → scrape each agent's `/metrics` every 5s → aggregate → push updates to browser via WebSocket

---

## Kubernetes Manifests

### DaemonSet (agent)
```yaml
nodeSelector:
  node-role.kubernetes.io/nat: "true"
spec:
  hostNetwork: true
  priorityClassName: system-node-critical
  terminationGracePeriodSeconds: 10
  securityContext:
    privileged: false
    capabilities:
      add: [NET_ADMIN, NET_RAW]
    readOnlyRootFilesystem: true
  resources:
    requests: {cpu: 50m, memory: 64Mi}
    limits:   {cpu: 200m, memory: 128Mi}
  readinessProbe: GET /readyz :9100   # ready only after route table is owned
  livenessProbe:  GET /healthz :9100  # periodSeconds: 10
  lifecycle:
    preStop: sleep 2                  # allows step-down signal to propagate
  volumes:
    - hostPath /proc/sys              # for sysctl
    - hostPath /lib/modules           # for iptables kernel modules
```

### Deployment (dashboard)
```yaml
replicas: 1
securityContext:
  runAsNonRoot: true
  readOnlyRootFilesystem: true
  capabilities.drop: [ALL]
args: ["dashboard", "--scrape-interval=5s", "--port=8080"]
```

### Supporting resources
- **PodDisruptionBudget**: `maxUnavailable: 0` on agent — forces Karpenter to drain gracefully
- **PriorityClass**: `system-node-critical` — survives node memory pressure eviction
- **NetworkPolicy**: agents allow inbound `:9100` (metrics) and `:9101` (peer) from namespace
- **ServiceMonitor**: Prometheus Operator compatible (optional)
- **Helm chart**: all manifests templated with `values.yaml`

### RBAC

**kube-nat-agent ServiceAccount**  
- `leases`: get, create, update (own AZ lease); list, watch (all leases)
- `pods`: list, watch (peer discovery)
- `nodes`: get (read own node AZ label)

AWS IAM (IRSA): `ec2:ModifyInstanceAttribute`, `ec2:DescribeRouteTables`, `ec2:ReplaceRoute`, `ec2:DescribeInstances`

**kube-nat-dashboard ServiceAccount**  
- `pods`: list, watch
- `leases`: list, watch

---

## Configuration

| Variable | Default | Description |
|---|---|---|
| `KUBE_NAT_MODE` | `auto` | `auto` or `manual` route table management |
| `KUBE_NAT_AZ_LABEL` | `topology.kubernetes.io/zone` | Node label for AZ discovery |
| `KUBE_NAT_LEASE_DURATION` | `15s` | Lease TTL (split-brain lock timeout) |
| `KUBE_NAT_PROBE_INTERVAL` | `200ms` | TCP keepalive probe interval |
| `KUBE_NAT_PROBE_FAILURES` | `2` | Consecutive failures before takeover |
| `KUBE_NAT_RECONCILE_INTERVAL` | `30s` | Reconciliation loop interval |
| `KUBE_NAT_METRICS_PORT` | `9100` | Prometheus metrics port |
| `KUBE_NAT_PEER_PORT` | `9101` | TCP peer health port |
| `KUBE_NAT_CONNTRACK_MAX` | _(kernel default)_ | Override nf_conntrack_max |
| `KUBE_NAT_IP_LOCAL_PORT_RANGE` | _(kernel default)_ | Override ephemeral port range |
| `KUBE_NAT_TAG_PREFIX` | `kube-nat` | AWS tag prefix for route table discovery |

---

## NAT Engine

- **Library**: `github.com/coreos/go-iptables` for rule management, `github.com/vishvananda/netlink` for interface stats
- **Rule**: `iptables -t nat -A POSTROUTING -o <iface> -j MASQUERADE` (same as fck-nat)
- **Idempotent**: reconciler checks rule existence before writing; safe to call on every loop
- **No privileged container**: `NET_ADMIN` + `NET_RAW` capabilities are sufficient

---

## Testing Strategy

- **Unit tests**: nat/, iface/, aws/, lease/, peer/ packages — all side-effect-free logic tested with interfaces/mocks
- **Integration tests**: spun up with real iptables in a Docker container with `NET_ADMIN`; AWS calls mocked via localstack or interface injection
- **E2E**: Helm chart deployed to a kind cluster with fake EC2 metadata server; verifies iptables rules, Lease writes, and failover sequence
- **Manual mode**: verified by checking stderr output matches expected `aws ec2` commands
