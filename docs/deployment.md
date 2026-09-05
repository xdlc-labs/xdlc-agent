# Deployment

Run the daemon where it can reach GitHub, inbound webhooks, and (for a real Fix) your coding-agent API. Argo, Prometheus, and `kubectl` are only required for the [optional GitOps / prod profiles](production-loop.md).

## Artifacts

| What | Name |
|------|------|
| Binary / ENTRYPOINT | `xdlc` |
| Image | `ghcr.io/xdlc-labs/xdlc-agent:<tag>` |
| Helm chart | `deploy/helm/xdlc-agent` |

Install the CLI with **[Install](install.md)** (`curl … | sh`). Below is cluster / container deploy. Start from `xdlc init` (CI Fix). Pass `--profile gitops` or `--profile full` only when those gates are wired.

## Docker

CI Fix:

```sh
docker run --rm -p 8080:8080 \
  -v "$PWD/config.yaml:/etc/xdlc-agent/config.yaml:ro" \
  -v xdlc-data:/var/lib/xdlc-agent \
  -e XDLC_API_TOKEN -e GITHUB_TOKEN -e GITHUB_WEBHOOK_SECRET \
  -e ANTHROPIC_API_KEY -e OPENAI_API_KEY -e CURSOR_API_KEY \
  ghcr.io/xdlc-labs/xdlc-agent:0.0.1-beta.2 \
  daemon --config /etc/xdlc-agent/config.yaml
```

Add `-e ARGOCD_WEBHOOK_SECRET` / `-e ALERTMANAGER_WEBHOOK_SECRET` only for GitOps / full profiles.

Persist `/var/lib/xdlc-agent` (audit DB `xdlc-agent-history.db`).

## Helm

Chart defaults match CI Fix: embedded config has only the `ci` gate, and `role.create` is **false** (no kubectl RBAC in the smoke namespace).

```sh
helm install xdlc-agent deploy/helm/xdlc-agent \
  --set image.tag=0.0.1-beta.2 \
  --set existingSecret=xdlc-agent-secrets \
  --set-file config=config.yaml
```

GitOps profile also needs `--set role.create=true` (and `role.namespace` = `gates.dev-smoke.namespace`). See [GitOps](gitops-argo.md).

**Single replica only** — audit DB is single-writer (bbolt) and the data PVC is RWO. Chart validation fails if `replicaCount != 1`.

Create the secret with tokens/webhook secrets your values expect (see chart `values.yaml` comments).

## Network

- Console + API: `:8080` (or `server.addr`)
- Ingress must allow GitHub to POST `/webhooks/github`. Argo / Alertmanager paths are only needed for optional profiles.
- Outbound: GitHub API, agent CLIs’ model APIs. `argocd` / `kubectl` / Prom only if those gates are enabled. Helm NetworkPolicy egress is DNS + TCP 443; in-cluster Prometheus on `:9090` needs the policy disabled or extended ([Prod health](prod-health.md)).

## From source

```sh
go build -o bin/xdlc ./cmd/xdlc-agent
./bin/xdlc daemon --config config.yaml
```

Embed the console: `cd ui && bun install && bun run build && cp -r dist/. ../internal/console/dist/`

## Verification status

Full local image build / GHCR pull: [#28](https://github.com/xdlc-labs/xdlc-agent/issues/28).
