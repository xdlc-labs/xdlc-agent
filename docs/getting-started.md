# Getting started

End-to-end first run for **xdlc**: personalize the template, bootstrap a
local cluster, wire GitHub webhooks, and watch the three-gate loop
(`xdlc-agent` binary).

## 1. Fork and clone

Click **Use this template** on GitHub (or fork), then:

```sh
git clone https://github.com/<you>/xdlc.git
cd xdlc
git checkout -b develop   # agent loop targets develop
git push -u origin develop
```

## 2. Personalize

```sh
./scripts/setup.sh
```

Prompts for GitHub org, repo name, and local domain. Writes `.env` and replaces `your-org` placeholders repo-wide. Creates `config.yaml` from `config.example.yaml` if missing.

## 3. Bootstrap local cluster

```sh
# Prefer GitHub App (see docs/deployment.md). PAT fallback:
export GITHUB_TOKEN=ghp_...          # repo + workflow scope
export ANTHROPIC_API_KEY=sk-ant-...  # for fix actions (or OPENAI_API_KEY / CURSOR_API_KEY, matching config.yaml's agent.provider)
export GITHUB_WEBHOOK_SECRET=$(openssl rand -hex 20)

./scripts/bootstrap-local.sh
```

This creates a Kind cluster and installs:

- Contour ingress
- ArgoCD
- Prometheus (kube-prometheus-stack with remote-write receiver)
- OTel Collector (`observability/otel/`)
- `example-service` in `dev` namespace
- smoke/e2e probe Job
- `xdlc-agent` in `xdlc` namespace (unless `--skip-agent`)

Options:

- `--skip-agent`: infra only, no agent deploy
- `--with-gitops`: apply `gitops/root-*.yaml` (requires repo pushed to GitHub)

Verify:

```sh
curl http://example-service.xdlc.local/healthz
# ok
```

Add to `/etc/hosts` if bootstrap printed a line:

```
127.0.0.1 example-service.xdlc.local
```

## 4. Wire GitHub webhook (CI gate)

The CI gate needs GitHub to POST `workflow_run` events to the agent.

**Local dev**: port-forward and tunnel:

```sh
kubectl port-forward -n xdlc svc/xdlc-agent 8080:8080 &
ngrok http 8080
```

In GitHub → Settings → Webhooks → Add webhook:

| Field | Value |
|---|---|
| Payload URL | `https://<ngrok-id>.ngrok.io/webhooks/github` |
| Content type | `application/json` |
| Secret | same as `GITHUB_WEBHOOK_SECRET` |
| Events | Workflow runs |

Push to `develop` on a configured repo triggers the webhook.

## 5. Watch the loop

### CI failure → fix

Break CI deliberately (e.g. failing test in `services/example-service`), push to `develop`. GitHub Actions fails, the webhook fires, the agent dispatches its coding-agent subagent (Claude Code / Codex / Cursor, per `config.yaml`'s `agent.provider`), which commits+pushes a fix (or leaves a note in `BACKLOG.md`).

```sh
kubectl logs -n xdlc deploy/xdlc-agent -f
./bin/xdlc-agent history    # if running locally
cat BACKLOG.md
```

### DEV smoke pass → promote

When ArgoCD syncs a healthy deploy and the k6 smoke Job exits 0, the dev-smoke poller emits pass and the agent fast-forwards `develop` → `main`.

Manual check:

```sh
./bin/xdlc-agent gate check dev-smoke --config config.yaml
```

### PROD breach → revert

If Prometheus p95 or error rate exceeds thresholds in `config.yaml`, the
prod-health poller (or Alertmanager webhook) emits breach and the agent
runs `git revert` on **main** (and aligns `develop` when tips matched).

## 6. Optional: ops console

```sh
kubectl port-forward -n xdlc svc/xdlc-agent 8080:8080 &
cd ui && bun install && bun run dev
```

Open http://127.0.0.1:5173 — see [console.md](console.md).

## 7. Validate config vs gitops

```sh
./bin/xdlc-agent validate --config config.yaml --gitops-dir gitops
```

Catches typos in `argocd_app` / `probe_job` before the gates silently never pass.

## Next steps

- [Architecture](architecture.md): loop design and what matches the diagram today
- [Console](console.md): ops UI
- [Service onboarding](service-onboarding.md): add another repo
- [Deployment](deployment.md): production Helm deploy
- [Runbook](runbook.md): agent behavior reference
- [Local setup](local-setup.md): troubleshooting Kind/bootstrap
- [HA](disaster-recovery.md#high-availability-not-implemented): the one known open gap (single replica, no leader election)
