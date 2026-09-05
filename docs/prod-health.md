# Prod health

PROD gate watches latency / error-rate (or your PromQL) and drives **Revert** on breach.

## Config

```yaml
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

## Alertmanager webhook (optional)

```text
POST /webhooks/alertmanager
```

```sh
export ALERTMANAGER_WEBHOOK_SECRET=...
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
