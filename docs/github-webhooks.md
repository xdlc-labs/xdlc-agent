# GitHub webhooks

CI gate input is primarily GitHub Actions `workflow_run` deliveries to the daemon.

## Auth to GitHub (API)

Prefer a **GitHub App** over a PAT:

```yaml
# config.yaml
github:
  app_id: 123456
  installation_id: 12345678
  private_key_env: GITHUB_APP_PRIVATE_KEY
```

Fallback: `GITHUB_TOKEN` in the environment. App needs rights to read Actions, contents, and (for Fix PR mode) pull requests.

## Webhook endpoint

Point the org/repo webhook at:

```text
https://<your-daemon-host>/webhooks/github
```

Events: at least **Workflow runs**. Content type JSON.

Set the webhook secret and export it:

```sh
export GITHUB_WEBHOOK_SECRET=...
```

In config (defaults shown in `config.example.yaml`):

```yaml
server:
  github_webhook_secret_env: GITHUB_WEBHOOK_SECRET
  require_webhook_secret: true   # prod: fail closed if secret unset
```

HMAC verification uses that env. With `require_webhook_secret: false`, missing secrets are allowed for local loopback only — do not run that on a public address.

## Repo mapping

Webhook payload repo (`owner/name`) must match a `repos[].github` entry:

```yaml
repos:
  - name: example-service          # config id (console Actions use this)
    github: your-org/example-service
    gates: [ci, dev-smoke, prod-health]
```

Failed runs on the integration branch enqueue **Fix** (after optional flake ladder: `ci_rerun_before_fix`, default on).

## Poller fallback

If webhooks are quiet, CI can still be observed via poll paths where configured — webhooks are the preferred edge.

## Verification status

End-to-end signed `workflow_run` → Fix on a real org: [#24](https://github.com/xdlc-labs/xdlc-agent/issues/24).
