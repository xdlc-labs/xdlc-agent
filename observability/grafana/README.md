# Grafana dashboards

Minimal dashboards for the xdlc / xdlc-agent stack. JSON is Grafana
schemaVersion 39 (Grafana 10+); older Grafana may rewrite on import.

## Import `xdlc-overview.json`

1. Open Grafana → **Dashboards** → **New** → **Import**.
2. Upload `observability/grafana/dashboards/xdlc-overview.json`
   (or paste the file contents).
3. Pick your Prometheus (or Mimir / VictoriaMetrics PromQL) datasource
   when prompted — the dashboard uses a `${datasource}` variable.
4. Click **Import**.

### Optional: provisioning

Drop the JSON into a Grafana sidecar / provisioning dir, e.g.:

```yaml
# grafana dashboard provider (snippet)
apiVersion: 1
providers:
  - name: xdlc
    folder: xdlc
    type: file
    options:
      path: /var/lib/grafana/dashboards/xdlc
```

Mount this directory (or the single JSON file) into that path.

### What the panels expect

| Panel | Series | Source |
|---|---|---|
| Daemon up | `up{job="xdlc-agent"}` | ServiceMonitor/PodMonitor scraping agent `GET /metrics` |
| Webhook / gate rates | `xdlc_agent_webhooks_total`, `xdlc_agent_gate_checks_total` | OTLP → collector remote_write (`internal/otel`) |
| Dispatch / subagent p95 | `xdlc_agent_*_duration_seconds_bucket` | same |
| Prod p95 / error rate | `http_request_duration_seconds_bucket`, `http_requests_total` | example-service / service-template (same exprs as `prometheus/rules/prod-health.yaml`) |
| Agent API request rate | `xdlc_agent_http_requests_total` | **placeholder** — not instrumented |

Apply companion alerts from `observability/prometheus/rules/`
(`prod-health.yaml`, `agent-health.yaml`).
