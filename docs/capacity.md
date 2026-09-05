# Capacity, scaling, and tenancy

Honest starting guidance for **one** `xdlc-agent` daemon. Not a load-test
report; numbers below are order-of-magnitude, not guarantees.

## Repos per instance

Plan for **dozens of repos**, not thousands, on a single process.

Why the ceiling is low today:

- The **prod-health poller walks every repo serially** each tick (one
  `Poller` over `Repos`, `Gate.Check` per repo in order, two PromQL
  queries each). More repos → longer ticks → slower edge detection for
  the last repos in the list, and once a tick outruns
  `gates.prod-health.interval` you are permanently behind.
- **Dev-smoke is the opposite shape**: one `Poller` goroutine *per repo*,
  all on the same interval with no jitter, each tick shelling out to
  `argocd` + `kubectl`. That removes the serial ceiling but replaces it
  with process-spawn load that grows linearly with repo count and
  arrives in aligned bursts.
- **`agent.max_concurrent_fixes` defaults to 2**. CI storms queue behind
  that semaphore; one slow Fix (up to `agent.timeout`, default 10m) holds
  a slot.
- Orchestrator dispatch and local state (clones, bbolt, `BACKLOG.md`) are
  sized for a single-tenant Deployment, not a mega-fleet in one binary.

If you outgrow dozens: **shard** — one daemon per tenant / trust domain
(see [Tenancy](#tenancy-one-daemon-per-trust-domain)) — rather than
stretching one config to the whole org.

## Per-process knobs

| Knob | Default | Scope |
|---|---|---|
| `server.webhook_rate_per_sec` / `webhook_rate_burst` | 20 / 40 | shared token-bucket on all `/webhooks/*` |
| `agent.max_concurrent_fixes` | 2 | global semaphore around `Dispatcher.Fix` |
| `gates.dev-smoke.interval` | `30s` | How often each smoke poller re-checks |
| `gates.prod-health.interval` | `30s` | How often the health poller walks repos |
| `agent.timeout` | `10m` | Max wall time for one Fix subagent run |

These shape *your* loop latency; they are not a published SLO from
maintainers. Webhooks (CI, Argo CD, Alertmanager) are the fast path when
wired; pollers are the fallback. Details: [gates.md](gates.md).

Multi-replica (`replicaCount` > 1) would multiply rate/Fix budgets and
race on local state (bbolt, `repos/`, `BACKLOG.md`). Keep
`replicaCount: 1` until leader election or a networked store lands — see
[disaster-recovery.md](disaster-recovery.md#high-availability-not-implemented).

## PVC sizing

Helm / local-dev default **`persistence.size: 2Gi`** is fine for a demo
with a few small clones. It is **too small** once you watch many or large
repos.

```text
PVC ≥ sum(working-tree sizes of every repo clone)
    + audit DB (bbolt) growth
    + BACKLOG.md headroom
    + spare for git fetch / temporary objects
```

Clones under `repos/` dominate. Measure with `du` on a warm PVC after a
full sync cycle, then add margin. Raise `persistence.size` (and the
StorageClass) before the volume fills — a full disk stalls Fix/Promote
and audit writes.

See [deployment.md](deployment.md#persistence).

## CPU and memory

Chart defaults (`deploy/helm/xdlc-agent/values.yaml`):

| | CPU | Memory |
|---|---|---|
| requests | `100m` | `128Mi` |
| limits | `500m` | `256Mi` |

Raise when:

- Several Fix subagents run near the concurrency cap (CLI + git are
  heavier than the Go poll loop).
- Many large clones cause page cache / git pressure (memory first).
- Continuous PromQL + k8s probe work overlaps with webhook bursts.

The Go daemon itself is light; **subagent CLIs and `git`** usually set the
floor. Watch pod throttling / OOMKilled and bump limits before raising
`max_concurrent_fixes`.

## Tenancy (one daemon per trust domain)

This is **not** multi-tenant-in-one-process. One process loads one config
and one credential set. Every repo in that `config.yaml` shares the same
GitHub App installation (or PAT) and the same LLM API key.

**Recommended:** one Helm release per tenant (team, BU, or blast-radius
boundary):

| Per tenant | Why |
|---|---|
| One Deployment | Separate process, crash domain, resource limits |
| One `config.yaml` | Repo list + gates scoped to that tenant |
| One Secret | Credentials never shared across tenants |
| One PVC | Audit DB, `BACKLOG.md`, clones stay isolated |

Prefer a dedicated namespace and a GitHub App **installation** scoped to
that tenant's repos — not one org-wide PAT for every team.

**Do not:**

- Point one daemon at every team's repos with one broad credential.
- Treat "many repos in one config" as multi-tenancy — that is still one
  trust domain.
- Expect `/api/*` to enforce tenant RBAC; isolation is deploy-time.

Horizontal coverage = many single-replica daemons (option C under
[HA](disaster-recovery.md#high-availability-not-implemented)), not one
shared HA service.

## Related

- Deploy persistence: [deployment.md](deployment.md#persistence)
- Backup / HA SPOF: [disaster-recovery.md](disaster-recovery.md)
- Credentials: [SECURITY.md](../SECURITY.md)
