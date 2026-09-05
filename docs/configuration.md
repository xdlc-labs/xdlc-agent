# Configuration

Primary file: `config.yaml` (schema: `schema/config.schema.json`). Starter: `config.example.yaml` or `xdlc init`.

Seed `repos:` from the checkouts already on this machine:

```sh
xdlc init --scan ~/src
```

Every Git checkout directly under that directory with a GitHub `origin` becomes a repo entry with the `ci` gate. Cluster keys (`argocd_app`, `probe_job`) are left commented for you to fill in.

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
  provider: claude   # claude | codex | cursor | gemini
  timeout: 10m
  # fix_mode: direct | pr
  # max_concurrent_fixes: 2
  # fix_budget: 5m
  # fix_reverify: true
  # ci_rerun_before_fix: true
  # fix_plan: true
  # rules_file: /etc/xdlc/rules.md   # applied to every repo
  # sessions:
  #   enabled: true
  #   dir: sessions
  #   retain: 720h
  #   max_file_bytes: 2097152
```

| Key | Default | What it does |
|-----|---------|--------------|
| `timeout` | `10m` in examples | Kill the coding-agent subprocess after this. Too short (e.g. 1m) for a real Cursor Manual Fix. |
| `rules_file` | none | Instructions file added to every Fix prompt, after the repo's own rules. [Rules and skills](rules-and-skills.md) |
| `sessions.enabled` | `true` | Record each Fix's prompt, output and diff to disk. [Fix sessions](sessions.md) |
| `sessions.dir` | `sessions` | Where those directories go. Mount it in containers to survive restarts. |
| `sessions.retain` | `720h` | Age after which a session is pruned. |
| `sessions.max_file_bytes` | 2 MiB | Cap per artifact. |

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
