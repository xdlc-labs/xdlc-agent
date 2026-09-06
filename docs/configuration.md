# Configuration

Primary file: `config.yaml` (schema: `schema/config.schema.json`). Starter: `config.example.yaml` or `xdlc init`.

## Profiles

| Profile | Command | `repos[].gates` |
|---------|---------|-----------------|
| **ci** (default) | `xdlc init` | `[ci]` |
| **gitops** | `xdlc init --profile gitops` | `[ci, dev-smoke]` |
| **full** | `xdlc init --profile full` | `[ci, dev-smoke, prod-health]` |

Seed `repos:` from the checkouts already on this machine (still CI-only unless you pass `--profile`):

```sh
xdlc init --scan ~/src
```

Every Git checkout directly under that directory with a GitHub `origin` becomes a repo entry with the `ci` gate. Cluster keys (`argocd_app`, `probe_job`) are left commented on profile `ci`.

Gates the daemon does not list on a repo do not run: no Argo poller, no PromQL, no extra webhook secrets.

## Server

CI Fix only needs the GitHub webhook secret:

```yaml
server:
  addr: ":8080"
  github_webhook_secret_env: GITHUB_WEBHOOK_SECRET
  require_webhook_secret: false  # true in prod
```

GitOps / full profiles also set `argocd_webhook_secret_env` and `alertmanager_webhook_secret_env`.

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
    gates: [ci]
```

GitOps adds `argocd_app` and `probe_job` ([GitOps](gitops-argo.md)). Optional on any profile: `depends_on`, `promote_requires`, `agent_instructions`.

## Gates

- **ci** — `trigger: on_push` (webhook-driven). Default install.
- **dev-smoke** — namespace + interval; Argo webhook / poller. [GitOps profile](gitops-argo.md).
- **prod-health** — `metrics_url`, thresholds, PromQL templates. [Full profile](prod-health.md).

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
  # fix_attempts: 2
  # ci_rerun_before_fix: true
  # fix_plan: true
  # worktree:
  #   enabled: false
  #   keep_failed: 24h
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
| `fix_reverify` | `false` | Re-check the failing gate after the agent exits, and only then record the Fix as ok. [Fix modes](fix-modes.md) |
| `fix_attempts` | `1` | Max agent runs per failing signal. Above 1 the agent is sent back in when the re-check is still red. Requires `fix_reverify`. [Fix modes](fix-modes.md) |
| `worktree.enabled` | `true` | Run each Fix in its own git worktree instead of the repo's shared clone. Lets two Fixes for one repo run at once, and keeps a crashed Fix out of the shared clone. [Fix modes](fix-modes.md) |
| `worktree.keep_failed` | `24h` | How long a failed Fix's worktree stays on disk for inspection. Successful ones are removed at once. |
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
