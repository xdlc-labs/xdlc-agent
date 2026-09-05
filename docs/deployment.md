# Deployment

Run the daemon where it can reach GitHub, your GitOps tools, Prometheus, and inbound webhooks.

## Artifacts

| What | Name |
|------|------|
| Binary / ENTRYPOINT | `xdlc` |
| Image | `ghcr.io/xdlc-labs/xdlc-agent:<tag>` |
| Helm chart | `deploy/helm/xdlc-agent` |

Install the CLI with **[Install](install.md)** (`curl … | sh`). Below is cluster / container deploy.

## Docker

```sh
docker run --rm -p 8080:8080 \
  -v "$PWD/config.yaml:/etc/xdlc-agent/config.yaml:ro" \
  -v xdlc-data:/var/lib/xdlc-agent \
  -e XDLC_API_TOKEN -e GITHUB_TOKEN -e GITHUB_WEBHOOK_SECRET \
  -e ARGOCD_WEBHOOK_SECRET -e ALERTMANAGER_WEBHOOK_SECRET \
  -e ANTHROPIC_API_KEY -e OPENAI_API_KEY -e CURSOR_API_KEY \
  ghcr.io/xdlc-labs/xdlc-agent:0.0.1-beta.2 \
  daemon --config /etc/xdlc-agent/config.yaml
```

Persist `/var/lib/xdlc-agent` (audit DB `xdlc-agent-history.db`).

## Helm

```sh
helm install xdlc-agent deploy/helm/xdlc-agent \
  --set image.tag=0.0.1-beta.2 \
  --set existingSecret=xdlc-agent-secrets \
  --set-file config=config.yaml
```

**Single replica only** — audit DB is single-writer (bbolt) and the data PVC is RWO. Chart validation fails if `replicaCount != 1`.

Create the secret with tokens/webhook secrets your values expect (see chart `values.yaml` comments).

## Network

- Console + API: `:8080` (or `server.addr`)
- Ingress must allow GitHub / Argo / Alertmanager to POST `/webhooks/*`
- Outbound: GitHub API, agent CLIs’ model APIs, Prom, `argocd`/`kubectl` if used

## From source

```sh
go build -o bin/xdlc ./cmd/xdlc-agent
./bin/xdlc daemon --config config.yaml
```

Embed the console: `cd ui && bun install && bun run build && cp -r dist/. ../internal/console/dist/`

## Verification status

Full local image build / GHCR pull: [#28](https://github.com/xdlc-labs/xdlc-agent/issues/28).
