# Secrets management

[deployment.md](deployment.md) shows `kubectl create secret generic …`
with `--from-literal` for a quick path. That works for Kind and first
bring-up. It is a hard stop for most enterprise reviews: GitHub App
private keys, LLM API keys, and webhook secrets sit in shell history and
often in CI logs.

This doc covers what the daemon needs and how to feed the same
Kubernetes Secret without pasting values on the CLI.

## What the daemon needs

Mount (or envFrom) one Secret the chart references via
`existingSecret` (default name in docs: `xdlc-agent-secrets`). Keys the
process actually reads:

| Key / env | Required? | Purpose |
|---|---|---|
| `GITHUB_APP_ID` | Prefer App | GitHub App ID |
| `GITHUB_APP_INSTALLATION_ID` | Prefer App | Installation ID for this tenant's repos |
| `GITHUB_APP_PRIVATE_KEY` or file via `GITHUB_APP_PRIVATE_KEY_FILE` | Prefer App | PEM private key; mint short-lived installation tokens |
| `GITHUB_TOKEN` | Fallback only | PAT when App unset; omit when App is configured |
| `GITHUB_WEBHOOK_SECRET` | Yes in prod | HMAC for `/webhooks/github` |
| `ARGOCD_WEBHOOK_SECRET` | If using ArgoCD webhook | Bearer / header for `/webhooks/argocd` |
| `ALERTMANAGER_WEBHOOK_SECRET` | If using AM webhook | Same for `/webhooks/alertmanager` |
| `XDL_API_TOKEN` | Yes for `/api/*` (operator) | Bearer for protected dashboard API; unset → 503 fail-closed |
| `XDL_API_VIEWER_TOKEN` | Optional | Read-only bearer (GETs only) |
| `OIDC_CLIENT_SECRET` | If `server.oidc.issuer_url` set | OAuth2 client secret for the console SSO flow |
| `OIDC_SESSION_SECRET` | If `server.oidc.issuer_url` set | HMAC key signing the session cookie — daemon refuses to start without it once OIDC is enabled |
| `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` / `CURSOR_API_KEY` | Matching `agent.provider` | Passed through to the subagent CLI allowlist only |

Env *names* for webhooks and API tokens are configurable in
`config.yaml` (`*_secret_env`, `api_token_env`, `oidc.client_secret_env`,
`oidc.session_secret_env`); defaults match the table.
See [SECURITY.md](../SECURITY.md) for blast radius and subagent env scrubbing.

One Secret set per tenant Deployment — see
[capacity.md](capacity.md#tenancy-one-daemon-per-trust-domain).

## Options beyond `kubectl create secret`

| Approach | Fit | Notes |
|---|---|---|
| **External Secrets Operator (ESO)** | **Recommended starting point** | Syncs from AWS Secrets Manager, GCP Secret Manager, Azure Key Vault, Vault, etc. into a normal K8s Secret the chart already consumes |
| Sealed Secrets | Good if git-only, no cloud SM | Encrypt secrets into Git; controller decrypts in-cluster. Ops model differs from cloud KMS |
| Vault Agent / Vault CSI | Good if Vault is already the org standard | Inject or mount at runtime; more moving parts than ESO for a first install |
| Raw `kubectl create secret` | Local / bootstrap only | Fine for Kind; avoid for internet-reachable or shared clusters |

Pick **one** path and stick to it. The daemon does not talk to Vault or
cloud SM itself — it only reads env vars / files from the mounted
Secret.

### Recommended starting point: External Secrets Operator

1. Install ESO in the cluster (upstream docs).
2. Create a `SecretStore` / `ClusterSecretStore` pointing at your backend
   (AWS SM example below).
3. Create an `ExternalSecret` that materializes `xdlc-agent-secrets`.
4. Point Helm at that Secret: `--set existingSecret=xdlc-agent-secrets`.

Sketch (AWS Secrets Manager; adjust names, region, and IRSA/role wiring
for your cluster):

```yaml
apiVersion: external-secrets.io/v1
kind: SecretStore
metadata:
  name: aws-secretsmanager
  namespace: xdlc
spec:
  provider:
    aws:
      service: SecretsManager
      region: us-east-1
      auth:
        jwt:
          serviceAccountRef:
            name: external-secrets   # IRSA-annotated SA
---
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: xdlc-agent-secrets
  namespace: xdlc
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: aws-secretsmanager
    kind: SecretStore
  target:
    name: xdlc-agent-secrets
    creationPolicy: Owner
  data:
    - secretKey: GITHUB_APP_ID
      remoteRef:
        key: xdlc/xdlc-agent
        property: GITHUB_APP_ID
    - secretKey: GITHUB_APP_INSTALLATION_ID
      remoteRef:
        key: xdlc/xdlc-agent
        property: GITHUB_APP_INSTALLATION_ID
    - secretKey: GITHUB_APP_PRIVATE_KEY
      remoteRef:
        key: xdlc/xdlc-agent
        property: GITHUB_APP_PRIVATE_KEY
    - secretKey: GITHUB_WEBHOOK_SECRET
      remoteRef:
        key: xdlc/xdlc-agent
        property: GITHUB_WEBHOOK_SECRET
    - secretKey: XDL_API_TOKEN
      remoteRef:
        key: xdlc/xdlc-agent
        property: XDL_API_TOKEN
    - secretKey: ANTHROPIC_API_KEY
      remoteRef:
        key: xdlc/xdlc-agent
        property: ANTHROPIC_API_KEY
    # Add ARGOCD_ / ALERTMANAGER_ webhook secrets and viewer token as needed.
    # If server.oidc.issuer_url is set, also add OIDC_CLIENT_SECRET and
    # OIDC_SESSION_SECRET the same way.
```

GCP Secret Manager / Vault backends look the same at the
`ExternalSecret` layer; only `SecretStore.spec.provider` changes.

This sketch is illustrative — pin ESO API versions and auth to whatever
your cluster runs. xdlc does not ship an ESO chart today, though the
cloud bootstrap starters under `infra/{aws-eks,gcp-gke,azure-aks}`
(shipped in 1.0.0) do stand up the cluster and its IAM.

## Honest limits

- Docs still lead with `kubectl create secret` for local bootstrap; that
  is intentional for Kind, not a recommendation for production.
- No built-in secret rotation hook: rotate in the backend, let ESO (or
  Sealed Secrets / Vault) refresh the K8s Secret, then restart or rely
  on your reload story (daemon reads env at process start).
- Subagent still needs an LLM API key in its scrubbed env; scoping that
  key in the vendor console matters as much as how you store it here.
