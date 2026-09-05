# Service onboarding

Add a new service repo to the xdlc loop.

## Overview

Each service needs:

1. **Source repo**: application code + CI on `develop`
2. **GitOps manifests**: ArgoCD Applications + value overlays in this repo
3. **Config entry**: `config.yaml` repo list
4. **Webhook**: GitHub `workflow_run` (one webhook covers all repos in the org)

## 1. Source repo

Scaffold from `services/example-service/` or copy the structure:

```
my-service/
  main.go
  Dockerfile
  .github/workflows/ci.yaml   # copy from templates/service-ci.yaml
```

CI on push to `develop` must:

1. Build + test
2. Push image to `ghcr.io/<org>/my-service:sha-<short>`
3. Commit tag update to `gitops/values/dev/my-service.yaml` in the gitops repo
   (use `secrets.GITOPS_TOKEN` PAT when the service repo ≠ gitops repo —
   `GITHUB_TOKEN` cannot push cross-repo; see `templates/service-ci.yaml`)

For a monorepo, see `.github/workflows/example-service-ci.yaml` (tag path
is excluded from `on.push.paths` so the write-back commit cannot loop).

## 2. GitOps manifests

Copy and rename the example files:

```sh
cp gitops/apps/dev/example-service.yaml gitops/apps/dev/my-service.yaml
cp gitops/apps/prod/example-service.yaml gitops/apps/prod/my-service.yaml
cp gitops/values/dev/example-service.yaml gitops/values/dev/my-service.yaml
cp gitops/values/prod/example-service.yaml gitops/values/prod/my-service.yaml
```

Edit:

- `metadata.name` to `dev-my-service` / `prod-my-service`
- value file paths to `my-service.yaml`
- image repository in value files

Apply (if using app-of-apps):

```sh
kubectl apply -f gitops/root-dev.yaml   # once
# ArgoCD picks up new file on next sync
```

## 3. Smoke probe

Copy probe Job if service needs custom checks:

```sh
cp observability/probes/smoke-e2e-job.yaml observability/probes/my-service-smoke.yaml
```

Update `TARGET_URL` or script. Wire via Helm PostSync hook or standalone apply.

## 4. config.yaml entry

```yaml
repos:
  - name: my-service
    github: your-org/my-service
    gates: [ci, dev-smoke, prod-health]
    argocd_app: dev-my-service
    probe_job: smoke-e2e
```

## 5. Validate

```sh
xdlc-agent validate --config config.yaml --gitops-dir gitops
```

Fixes typos in `argocd_app` before dev-smoke silently never passes.

## 6. Restart agent

If daemon already running, restart to pick up config + pre-clone new repo:

```sh
kubectl rollout restart -n xdlc deploy/xdlc-agent
```

## Checklist

- [ ] CI builds image on `develop` push
- [ ] CI writes tag to `gitops/values/dev/<service>.yaml`
- [ ] ArgoCD Application syncs to `dev` namespace
- [ ] Smoke Job passes
- [ ] Repo listed in `config.yaml`
- [ ] `validate` passes
- [ ] GitHub webhook delivers `workflow_run` events
