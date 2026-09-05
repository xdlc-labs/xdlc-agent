# GitOps layout

**xdlc** ships both the orchestrator and the central GitOps tree in one
repo. Service repos are separate.

## Two kinds of repo

- **Service repos** (×N, one per service: `landing`, `wiki`, `api`, ...):
  application source only. CI (`.github/workflows`) builds an image on
  push to `develop` and writes the resulting tag back to this repo's
  `gitops/values/{dev,prod}/<service>.yaml`.
- **This repo** (`xdlc`): the orchestrator *and* the central
  GitOps repo: `helm/service-template`, `gitops/apps/{dev,prod}`, and
  the per-service value overlays ArgoCD actually syncs from. One repo,
  two roles; fine at template scale, split them if the orchestrator and
  the GitOps config need different access control at your org.

This mirrors the diagram: N service repos push to their own `develop`,
GitHub Actions builds+tests, the image tag write-back is what actually
lands in `gitops/`, and *that* commit is what ArgoCD's `develop`-tracked
Application picks up.

**Green CI → DEV is not an agent action.** A successful Actions run writes
the image tag into `gitops/values/dev/<service>.yaml` and pushes; ArgoCD
(tracking `develop`, auto-sync) deploys it. The agent only reacts to
*failed* CI (Fix) and to smoke/prod gate signals.

## Layout

```
helm/service-template/       # one chart, every service deploys through it
gitops/
  values/dev.yaml            # shared DEV overrides (replicas, resources, ingress)
  values/prod.yaml           # shared PROD overrides
  values/dev/<service>.yaml  # per-service image tag, CI-written
  values/prod/<service>.yaml # per-service image tag, promote-carried
  apps/dev/<service>.yaml    # ArgoCD Application, tracks develop
  apps/prod/<service>.yaml   # ArgoCD Application, tracks main
  root-dev.yaml              # app-of-apps: discovers apps/dev/* (Kind default)
  root-prod.yaml             # app-of-apps: discovers apps/prod/* (Kind default)
  clusters/                  # multi-cluster ApplicationSet path
    README.md
    applicationset-root.yaml # list generator → root per logical cluster
    local-dev/               # optional per-cluster tree (apps still in apps/)
observability/
  probes/smoke-e2e-job.yaml  # PostSync Job the dev-smoke gate polls
  prometheus/rules/          # PrometheusRule mirroring gates.prod-health thresholds
```

## Bootstrap order

1. `kubectl apply -f gitops/root-dev.yaml -f gitops/root-prod.yaml` (once, against a cluster with ArgoCD installed)
2. Per new service: copy `gitops/apps/dev/example-service.yaml` +
   `gitops/apps/prod/example-service.yaml`, swap the name; copy
   `gitops/values/{dev,prod}/example-service.yaml` for the image
   tag file CI will write to; add the repo to `config.yaml`.
3. `xdlc-agent daemon` picks it up from `config.yaml` for gating; ArgoCD
   picks it up from `gitops/apps/` for deployment. The two are wired
   together only by convention (repo name / Application name /
   `argocd_app` field), no shared source of truth beyond `config.yaml`
   and this directory, which is deliberate: no extra service to run.

## Multi-cluster

Today Kind runs **dev and prod as namespaces on one cluster**. Destination
server on every root/app is `https://kubernetes.default.svc`.

For more than one registered ArgoCD cluster (or one generator for many
roots), use the ApplicationSet instead of `root-*.yaml`:

```bash
kubectl apply -f gitops/clusters/applicationset-root.yaml
```

`gitops/clusters/applicationset-root.yaml` uses a **list generator** (not
the cluster-secret API) with:

| cluster       | destination server                 | apps path          |
|---------------|------------------------------------|--------------------|
| `local-dev`   | `https://kubernetes.default.svc`   | `gitops/apps/dev`  |
| `local-prod`  | `https://kubernetes.default.svc`   | `gitops/apps/prod` |

That emits the same roots Kind already uses. To add a real region later:

1. Register the cluster in ArgoCD (`argocd cluster add …`).
2. Uncomment / add a list element, e.g. `us-east-1` with the cluster's
   API URL and the apps path it should sync.
3. Commit; ArgoCD creates `root-us-east-1`.

Details: [`gitops/clusters/README.md`](../gitops/clusters/README.md).

**Do not** apply both `root-*.yaml` and the ApplicationSet for the same
logical roots — duplicate Applications. Kind bootstrap
(`scripts/bootstrap-local.sh --with-gitops`) stays on `root-*.yaml` only.

Per-cluster directories under `gitops/clusters/<name>/` exist as placeholders;
apps remain in `gitops/apps/{dev,prod}` until a cluster needs a divergent set.

## Why the image tag lives in a separate file per service

So CI can write DEV without touching PROD, and promote can copy the
gated tag across then fast-forward:

1. `CarryProdTag` copies `image.tag` from `values/dev/<service>.yaml` →
   `values/prod/<service>.yaml` (no-op if `gitops/` absent in the clone)
2. Commit + push on `develop`
3. `git push origin develop:main` — ArgoCD prod syncs as a side-effect

Monorepo / gitops-in-same-clone is the path this template optimizes for.
Separate service repos still need CI to write the gitops repo (use
`GITOPS_TOKEN` PAT — see `templates/service-ci.yaml`); promote then runs
against the gitops-capable clone listed in `config.yaml`.
