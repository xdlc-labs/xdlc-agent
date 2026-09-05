# Configuration

Primary file: `config.yaml` (schema: `schema/config.schema.json`). Starter: `config.example.yaml` or `xdlc init`.

## Server

```yaml
server:
  addr: ":8080"
  github_webhook_secret_env: GITHUB_WEBHOOK_SECRET
  argocd_webhook_secret_env: ARGOCD_WEBHOOK_SECRET
  alertmanager_webhook_secret_env: ALERTMANAGER_WEBHOOK_SECRET
  require_webhook_secret: false  # true in prod
```

API tokens are **fixed env names** (not configurable in YAML). You **generate** the secret yourself — see [API tokens](api-tokens.md).

| Env | Role |
|-----|------|
| `XDLC_API_TOKEN` | Operator bearer (required for console API) |
| `XDLC_API_VIEWER_TOKEN` | Optional read-only bearer |

## Repos

```yaml
repos:
  - name: example-service       # id used by API / Actions
    github: your-org/example-service
    gates: [ci, dev-smoke, prod-health]
    argocd_app: dev-example-service
    probe_job: smoke-e2e
```

Optional: `depends_on`, `promote_requires`, `agent_instructions`.

## Gates

- **ci** — `trigger: on_push` (webhook-driven)
- **dev-smoke** — namespace + interval; Argo webhook / poller
- **prod-health** — `metrics_url`, thresholds, PromQL templates

## Agent

```yaml
agent:
  mode: subprocess
  provider: claude   # claude | codex | cursor
  timeout: 10m
  # fix_mode: direct | pr
  # max_concurrent_fixes: 2
  # fix_budget: 5m
  # fix_reverify: true
  # ci_rerun_before_fix: true
  # fix_plan: true
```

## Fleet (optional)

```yaml
# fleet:
#   flap_max_cycles: 3
#   circuit_breach_ratio: 0.3
#   notify_webhook_url: https://hooks.slack.com/...
#   patient_zero: true
```

## Validate

```sh
./bin/xdlc validate --config config.yaml
```
