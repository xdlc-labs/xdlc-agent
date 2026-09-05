# Local setup (Kind / Minikube)

Troubleshooting and day-2 operations for the local bootstrap paths.

## Minikube (alternative to Kind)

```sh
./scripts/setup.sh
./scripts/bootstrap-minikube.sh
```

Uses `minikube image build` (no `kind load`), published `ghcr.io/xdlc-labs/xdlc-agent:2.0.0` for the agent (image tags carry no leading `v`; see [CHANGELOG.md](../CHANGELOG.md) for what's in the latest tag — bump this alongside `scripts/bootstrap-minikube.sh`'s pinned `image.tag` on release), and NodePort for example-service when ingress is slow.

| Flag | Effect |
|---|---|
| `--skip-agent` | Infra + example-service only |
| `--skip-prometheus` | Skip kube-prometheus-stack (lighter) |

**Rootless podman:** if `kube-proxy` crashes with `too many open files`, free disk on `/home` (podman storage defaults there) or relocate storage:

```sh
mkdir -p /var/tmp/xdlc-podman/{storage,run}
export CONTAINERS_STORAGE_CONF=/var/tmp/xdlc-storage.conf  # see scripts/bootstrap-minikube.sh
minikube delete --all --purge
minikube start --driver=podman --memory=8192
```

Prefer Docker driver when available: `minikube start --driver=docker --memory=8192`.

## Prerequisites

Run `./scripts/verify-prereqs.sh`, checks git, docker, kind, kubectl, helm.

**Machine sizing:** 8 CPU / 16 GB RAM recommended. Bootstrap installs ArgoCD + Prometheus + two services, roughly 6-8 GB Docker usage.

## Kind bootstrap (default)

```sh
./scripts/setup.sh
./scripts/bootstrap-local.sh
```

### Flags

| Flag | Effect |
|---|---|
| `--skip-agent` | Cluster + example-service only |
| `--with-gitops` | Apply ArgoCD app-of-apps (needs repo on GitHub) |

### Re-create cluster

Also the fix when Kind breaks after a Docker restart:

```sh
kind delete cluster --name xdlc
./scripts/bootstrap-local.sh
```

## /etc/hosts

Ingress uses `*.xdlc.local` (or your `DOMAIN` from `.env`):

```
127.0.0.1 example-service.xdlc.local
```

Bootstrap prints the exact line if not already present.

## Access URLs

| Service | Access |
|---|---|
| example-service | `http://example-service.xdlc.local/healthz` |
| ArgoCD UI | `kubectl port-forward svc/argocd-server -n argocd 8081:443` |
| Prometheus | `kubectl port-forward svc/kube-prometheus-kube-prome-prometheus -n monitoring 9090:9090` |
| xdlc-agent | `kubectl port-forward svc/xdlc-agent -n xdlc 8080:8080` |
| ops console | port-forward agent, then `cd ui && bun run dev` → http://127.0.0.1:5173 ([console.md](console.md)) |

ArgoCD admin password:

```sh
kubectl -n argocd get secret argocd-initial-admin-secret \
  -o jsonpath='{.data.password}' | base64 -d && echo
```

## GitHub webhooks locally

GitHub cannot reach `localhost`. Options:

1. **ngrok**: `ngrok http 8080` after port-forwarding xdlc-agent
2. **smee.io**: GitHub → smee → local forwarder
3. **Skip webhook**: use `xdlc-agent gate check ci` in CI only; dev/prod gates still poll

## Common issues

| Issue | Fix |
|---|---|
| `kind create cluster` hangs | Check Docker resources; restart Docker |
| Ingress 404 | Wait for Contour/Envoy rollout; check `/etc/hosts` |
| ImagePullBackOff on example-service | Re-run bootstrap (loads `example-service:local` into Kind) |
| Agent pre-clone failed | Set `GITHUB_TOKEN`; repo must exist on GitHub |
| Smoke Job failing | `kubectl logs -n dev job/smoke-e2e`; check service DNS |
| Prometheus unreachable from agent | Use in-cluster URL from bootstrap output |

## Local registry (optional)

Kind config includes mirror for `localhost:5001`. For iterative image builds without reload:

```sh
docker tag example-service:local localhost:5001/example-service:local
docker push localhost:5001/example-service:local
```

Not required for default bootstrap flow (`kind load docker-image`).
