# Prod health

**Opt-in profile.** Skip this until CI Fix (and usually GitOps promote) already work. Enable with `xdlc init --profile full` or by adding `prod-health` to `repos[].gates`.

PROD gate watches latency / error-rate (or your PromQL) and drives **Revert** on breach.

## Config

Add the gate on the repo **and** fill `gates.prod-health` (validate fails if a repo lists the gate but PromQL is empty):

```yaml
repos:
  - name: example-service
    github: your-org/example-service
    gates: [ci, dev-smoke, prod-health]
    argocd_app: dev-example-service
    probe_job: smoke-e2e

gates:
  prod-health:
    trigger: continuous
    metrics_url: http://prometheus.your-domain.io
    thresholds:
      p95_ms: 500
      error_rate: 0.01
    interval: 30s
    p95_query: histogram_quantile(0.95, sum(rate(http_request_duration_seconds_bucket{service="{{repo}}"}[5m])) by (le)) * 1000
    error_rate_query: sum(rate(http_requests_total{service="{{repo}}",status=~"5.."}[5m])) / sum(rate(http_requests_total{service="{{repo}}"}[5m]))
```

`{{repo}}` is substituted per managed repo. Any PromQL instant-query API works (Prometheus, compatible stores).

Helm `networkPolicy` (on by default) allows egress to DNS and **TCP 443 only**. In-cluster Prometheus on `:9090` will not work until you disable the policy (`networkPolicy.create: false`) or extend egress. Prefer the Alertmanager webhook below if you can.

## Alertmanager webhook (optional)

```text
POST /webhooks/alertmanager
```

```sh
export ALERTMANAGER_WEBHOOK_SECRET=...
```

```yaml
server:
  alertmanager_webhook_secret_env: ALERTMANAGER_WEBHOOK_SECRET
```

Poller remains the continuous fallback.

## Revert

On breach, policy selects **Revert**: `git revert` on `main` (prod), with develop realigned when it still pointed at the pre-revert tip. Fleet knobs (`circuit_breach_ratio`, `patient_zero`, flap) can suppress or redirect — see [Architecture](architecture.md).

## Doctor

```sh
./bin/xdlc doctor --config config.yaml   # omit --skip-network to probe Prom
```

## Verification status

Controlled live breach → Revert: [#26](https://github.com/xdlc-labs/xdlc-agent/issues/26). Gate logic is covered by package tests; demo includes a prod-breach scenario with stubs.
