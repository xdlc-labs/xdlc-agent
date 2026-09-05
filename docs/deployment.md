# Deployment

Production deployment of **xdlc**'s `xdlc-agent` via Helm.

## Helm install

```sh
kubectl create namespace xdlc

kubectl create secret generic xdlc-agent-secrets \
  --namespace xdlc \
  --from-literal=GITHUB_WEBHOOK_SECRET=... \
  --from-literal=ANTHROPIC_API_KEY=sk-ant-...   # or OPENAI_API_KEY / CURSOR_API_KEY, matching agent.provider
# Prefer GitHub App (omit GITHUB_TOKEN when App is set):
#   --from-literal=GITHUB_APP_ID=123456 \
#   --from-literal=GITHUB_APP_INSTALLATION_ID=12345678 \
#   --from-file=GITHUB_APP_PRIVATE_KEY=./app.pem
# Or PAT fallback:
#   --from-literal=GITHUB_TOKEN=ghp_...
# Optional gate webhooks:
#   --from-literal=ARGOCD_WEBHOOK_SECRET=... \
#   --from-literal=ALERTMANAGER_WEBHOOK_SECRET=...

# Production: prefer External Secrets Operator (or Sealed Secrets / Vault)
# instead of --from-literal on the CLI — see [secrets.md](secrets.md).
# Multi-team orgs: one Deployment + Secret + PVC per tenant — see
# [capacity.md](capacity.md#tenancy-one-daemon-per-trust-domain).

helm upgrade --install xdlc-agent deploy/helm/xdlc-agent \
  --namespace xdlc \
  --set-file config=config.yaml \
  --set existingSecret=xdlc-agent-secrets \
  --set image.tag=2.0.0   # no leading "v" — GoReleaser tags are X.Y.Z; see CHANGELOG.md
```

## Config

Mount your real `config.yaml` via `--set-file config=...`. Minimum fields:

**`require_webhook_secret` default trap:** the chart's own `values.yaml`
defaults `server.require_webhook_secret` to `true`, but that default only
applies to the config the chart would render itself — `--set-file
config=config.yaml` replaces the rendered config wholesale. If your
`config.yaml` was copied from `config.example.yaml` (which ships `false`,
the local-Kind-friendly default) and you don't override it, you deploy with
webhook signature verification **skipped**, not enforced. Set it explicitly
to `true` in any `config.yaml` you mount into this chart.

```yaml
repos:
  - name: my-service
    github: your-org/my-service
    gates: [ci, dev-smoke, prod-health]
    argocd_app: dev-my-service
    probe_job: smoke-e2e

server:
  addr: ":8080"
  require_webhook_secret: true

gates:
  dev-smoke:
    namespace: dev
    interval: 30s
  prod-health:
    metrics_url: http://prometheus.monitoring.svc:9090 # any PromQL API
    interval: 30s
    thresholds:
      p95_ms: 500
      error_rate: 0.01

agent:
  mode: subprocess
  provider: claude # claude | codex | cursor
  timeout: 10m
```

Run `xdlc-agent validate --config config.yaml --gitops-dir gitops` before
deploy. Add `--role-namespace <value>` matching the chart's `role.namespace`
(default `dev`) to also catch it drifting from `gates.dev-smoke.namespace` —
nothing else cross-checks these two independently-set values, and a
mismatch fails as a confusing dev-smoke permission error rather than a
config error.

## GitHub App setup

1. Create a GitHub App (org settings) with Contents read/write and Actions read.
2. Install it on the repos listed in `config.yaml`.
3. Put App ID, installation ID, and private key PEM into the agent Secret
   (see above). Daemon logs `github auth source=app` on start.
4. Leave `GITHUB_TOKEN` unset when App is configured.

## Webhooks

Expose the agent Service (Ingress or LoadBalancer):

| Path | Source | Secret env |
|---|---|---|
| `/webhooks/github` | GitHub `workflow_run` | `GITHUB_WEBHOOK_SECRET` (HMAC) |
| `/webhooks/argocd` | ArgoCD notifications | `ARGOCD_WEBHOOK_SECRET` |
| `/webhooks/alertmanager` | Alertmanager | `ALERTMANAGER_WEBHOOK_SECRET` |

Only `workflow_run` events on `develop` produce CI signals. ArgoCD and
Alertmanager webhooks are the fast path; pollers still run as fallback.

Alertmanager alerts should carry a `repo` or `service` label matching the
config short name (see `observability/prometheus/rules/prod-health.yaml`).

**Alerting / threshold sync (known limitation):** the p95/error-rate
numbers in `observability/prometheus/rules/prod-health.yaml` are hand-
copied from `config.yaml`'s `gates.prod-health.thresholds` — there's no
templating from one source, by design (out of scope; see docs/gates.md
for the backlog idea). If you change `p95_ms`/`error_rate`/`interval`
in `config.yaml`, update that PrometheusRule file too, or the human-
facing Alertmanager alert and the agent's own auto-revert threshold will
silently drift apart. That file's header comment cross-references the
exact `config.yaml`/`internal/config/config.go` lines to keep in sync.
It also carries two agent-self-health alerts fed by `xdlc-agent`'s own
`/metrics` (not by the org's Prometheus scraping the *services*):
`ProdHealthPollerStale` (a poller hasn't ticked in a while — the
auto-revert safety net may be blind) and `ProdHealthStoreErrors` (the
bbolt audit DB on the PVC — see Persistence below — is failing to
read/write).

## Dashboard API

Same Service / port as webhooks.

| Path | Method | Purpose |
|---|---|---|
| `/api/health` | GET | liveness-ish JSON, unauthenticated |
| `/api/whoami` | GET | caller's own role (`operator`/`viewer`) — lets the console show/hide write actions |
| `/api/overview` | GET | repos + recent events |
| `/api/history` | GET | audit trail (`?repo=` filter) |
| `/api/backlog` | GET | `BACKLOG.md` (`?repo=` filter) |
| `/api/repos` | GET | configured repos |
| `/api/actions/{fix,promote,revert}` | POST | operator-only manual dispatch, requires `{"repo":"<name>","confirm":true}` |
| `/metrics` | GET | Prometheus scrape (open, no auth) |

## Agent `/metrics`

`GET /metrics` is on the same mux as webhooks/API — **open, no auth**
(Prometheus scrape convention). Same OTel instruments as OTLP export
(`xdlc_agent_webhooks_total`, `xdlc_agent_gate_checks_total`,
`xdlc_agent_*_duration_seconds`). Works even with `OTEL_SDK_DISABLED=true`
(in-process Prometheus registry only).

```bash
curl -s http://localhost:8080/metrics | grep xdlc_agent_
```

Point a ServiceMonitor/PodMonitor at the agent Service path `/metrics`,
or scrape `server.addr` directly. Rules: `observability/prometheus/rules/agent-health.yaml`.

Local UI: [console.md](console.md). Set `XDL_API_TOKEN` (or the env named
by `server.api_token_env`) and send `Authorization: Bearer <token>` on
all `/api/*` except `GET /api/health`. Unset token → protected routes
return 503 (fail closed).

## Metrics backends (PromQL)

`gates.prod-health.metrics_url` is any PromQL instant-query base URL.
`prometheus_url` is a legacy alias, still accepted when `metrics_url` is
empty — use `metrics_url` in new configs.

| Backend | Example URL |
|---|---|
| Prometheus | `http://prometheus.monitoring.svc:9090` |
| VictoriaMetrics | `http://victoria-metrics.monitoring.svc:8428` |
| OpenObserve | `http://openobserve.monitoring.svc:5080/api/.../prometheus` |
| Grafana Mimir | `http://mimir.monitoring.svc:9009/prometheus` |

Apps export OTLP to the Collector (`observability/otel/collector.yaml`).
Swap the Collector exporter for VictoriaMetrics remote_write or OpenObserve
OTLP — see comments in that ConfigMap. Gate queries stay PromQL.

## RBAC

Default chart RBAC: namespaced `Role` in `role.namespace` (default `dev`) for reading Jobs and Pod logs (dev-smoke probe inspection). Extend `role.rules` if the agent needs more access in that namespace.

ArgoCD and kubectl run as binaries in the container, configure kubeconfig or in-cluster ServiceAccount as your setup requires.

## Persistence

PVC (default 2Gi) holds:

- `BACKLOG.md`
- `xdlc-agent-history.db` (bbolt audit)
- `repos/` clones

Back up the PVC for audit continuity.

Keep `replicaCount: 1` until leader election lands (see
[disaster-recovery.md](disaster-recovery.md#high-availability-not-implemented)):
webhook rate limits and `max_concurrent_fixes` are per-process only. See
[capacity.md](capacity.md).

## Token scoping

Prefer a GitHub App scoped to the listed repos. If using `GITHUB_TOKEN`,
needs read+write on every repo in `config.yaml`. Scope narrowly, not
org-wide if avoidable. See [SECURITY.md](../SECURITY.md). For how secrets
land in-cluster without `kubectl create secret --from-literal`, see
[secrets.md](secrets.md). Isolation across teams is one daemon per
tenant, not shared credentials — [capacity.md](capacity.md#tenancy-one-daemon-per-trust-domain).

## Bring your own cluster

Skip local bootstrap; install prerequisites yourself:

1. ArgoCD + gitops manifests from this repo
2. PromQL-compatible store reachable at `gates.prod-health.metrics_url`
3. Optional: OTel Collector (`observability/otel/collector.yaml`)
4. Helm release of `deploy/helm/xdlc-agent`

Local bootstrap (`scripts/bootstrap-local.sh`) is a reference implementation, not required for production. Cloud starters (Terraform, `terraform fmt`-clean but never `apply`'d against a live account): `infra/aws-eks/` + `scripts/bootstrap-cloud/aws.sh`, `infra/gcp-gke/` + `scripts/bootstrap-cloud/gcp.sh`, `infra/azure-aks/` + `scripts/bootstrap-cloud/azure.sh`.

## Docker image

Published to `ghcr.io/xdlc-labs/xdlc-agent` on tag push. Bundles `git`,
`kubectl`, `argocd`, Claude Code CLI, and Codex CLI, required for
daemon operation. Cursor CLI isn't bundled by default (not
npm-installable), see [architecture.md](architecture.md#container-image).

Release tags also publish an SPDX SBOM asset and **keyless cosign**
signatures (GitHub OIDC → Sigstore). Verify:

```sh
cosign verify \
  --certificate-identity-regexp 'https://github.com/xdlc-labs/xdlc-agent/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/xdlc-labs/xdlc-agent:X.Y.Z
```

Image tags match GoReleaser `{{ .Version }}` (no leading `v`); GitHub
release tags stay `vX.Y.Z`.

Build locally:

```sh
docker build -t xdlc-agent:local -f deploy/Dockerfile .
```
