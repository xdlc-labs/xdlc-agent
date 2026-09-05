# GitOps / ArgoCD

Promote and DEV smoke assume you already ship **green build → DEV** via GitOps. The agent reacts to gates; it does not replace ArgoCD.

## Repo fields

```yaml
repos:
  - name: example-service
    github: your-org/example-service
    gates: [ci, dev-smoke, prod-health]
    argocd_app: dev-example-service
    probe_job: smoke-e2e
```

- `argocd_app` — Argo application name used for sync/status signals
- `probe_job` — Job/name used by the DEV smoke gate

## Webhook

```text
POST /webhooks/argocd
```

```sh
export ARGOCD_WEBHOOK_SECRET=...
```

```yaml
server:
  argocd_webhook_secret_env: ARGOCD_WEBHOOK_SECRET
```

Poller (`gates.dev-smoke.interval`) remains a fallback when notifications are quiet.

## Promote

On DEV smoke **pass**, policy selects **Promote**: fast-forward `develop` → `main` only (refused if not FF-able). Image tag carry (dev values → prod values) runs where GitOps layout expects it — see architecture notes on tag write-back.

Manual Promote is available in the console **Actions** tab (operator token).

## Verification status

Argo app sync / image tag write-back on a real cluster: [#25](https://github.com/xdlc-labs/xdlc-agent/issues/25). Git FF promote is covered by `xdlc demo` and Manual Promote.
