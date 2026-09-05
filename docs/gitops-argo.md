# GitOps / ArgoCD

**Opt-in profile.** The default install is CI Fix only ([Getting started](getting-started.md)). Enable this when you already ship **green build → DEV** via GitOps and want DEV smoke → **Promote**. The agent reacts to gates; it does not replace ArgoCD.

```sh
xdlc init --profile gitops
# or add "dev-smoke" to repos[].gates on an existing config
```

## Repo fields

```yaml
repos:
  - name: example-service
    github: your-org/example-service
    gates: [ci, dev-smoke]
    argocd_app: dev-example-service
    probe_job: smoke-e2e
```

- `argocd_app` — Argo application name used for sync/status signals
- `probe_job` — Job name used by the DEV smoke gate (`kubectl` in `gates.dev-smoke.namespace`)

Do not add `prod-health` until you are on the [full profile](prod-health.md).

## Helm

The chart defaults to CI Fix: `role.create` is **false** (no kubectl RBAC). Turn it on and keep `role.namespace` equal to `gates.dev-smoke.namespace` (default `dev`):

```sh
helm upgrade xdlc-agent deploy/helm/xdlc-agent \
  --set role.create=true \
  --set role.namespace=dev \
  --set-file config=config.yaml
```

`xdlc validate --config config.yaml --role-namespace dev` catches a mismatch that would otherwise show up as a mysterious smoke-gate permission error.

The pod still shells out to `argocd` and `kubectl` for this profile, so those CLIs must be authenticated in the container (image already bundles them).

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

On DEV smoke **pass**, policy selects **Promote**: fast-forward `develop` → `main` only (refused if not FF-able). Image tag carry copies `image.tag` from `gitops/values/dev/<service>.yaml` into `gitops/values/prod/<service>.yaml` when those files exist — otherwise promote is git FF only. Manual Promote is on the console **Actions** tab.

## Verification status

Argo app sync / image tag write-back on a real cluster: [#25](https://github.com/xdlc-labs/xdlc-agent/issues/25). Git FF promote is covered by `xdlc demo` and Manual Promote.
