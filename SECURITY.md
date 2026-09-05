# Security

## Reporting a vulnerability

Please report security issues privately via [GitHub's private
vulnerability reporting](https://github.com/xdlc-labs/xdlc-agent/security/advisories/new)
rather than a public issue. If that's not available, open an issue
asking a maintainer to reach out; don't post exploit details publicly.

## What this daemon has access to

`xdlc-agent daemon` is deliberately privileged: that's the point of an
agent that fixes, reverts, and promotes code. Understand what you're
granting it before running it:

- **GitHub App (preferred)**: set `GITHUB_APP_ID`,
  `GITHUB_APP_INSTALLATION_ID`, and `GITHUB_APP_PRIVATE_KEY` (PEM) or
  `GITHUB_APP_PRIVATE_KEY_FILE`. The daemon mints short-lived installation
  tokens for API + git push. Grant the App Contents read/write and
  Actions read on the repos in `config.yaml` only.
- **`GITHUB_TOKEN` (fallback)**: used when App credentials are unset.
  Read access (workflow_run status) and write access (git push, via
  `internal/repos.AuthEnv`) to every repo in `config.yaml`. Scope it to
  exactly those repos, not an org-wide PAT.
- **Webhook secrets**:
  - `GITHUB_WEBHOOK_SECRET` — HMAC for `/webhooks/github`
  - `ARGOCD_WEBHOOK_SECRET` — bearer / `X-Webhook-Secret` for `/webhooks/argocd`
  - `ALERTMANAGER_WEBHOOK_SECRET` — same for `/webhooks/alertmanager`
  With `server.require_webhook_secret: true` (Helm default), requests
  are refused when the relevant secret env is unset. With it false
  (local Kind default), missing secrets log a warning and skip
  verification — fine for a tunnel only you control, never for anything
  internet-reachable.
- **The coding-agent subprocess** (`internal/subagent`, Claude Code /
  Codex / Cursor / whatever `agent.provider` names) runs with whatever
  filesystem/network access the daemon process has, inside the repo
  clone directory. It can, by design, edit code and push. There is no
  sandboxing beyond what your container/host already provides; the
  Docker image (`deploy/Dockerfile`) doesn't drop privileges or restrict
  network egress, add that at the deployment layer if you need it.
  **The runner scrubs `cmd.Env` to an exact-name allowlist** before exec
  (`internal/subagent/runner.go`): `PATH`, `HOME`, `USER`, `LOGNAME`,
  `LANG`, `LC_ALL`, `LANGUAGE`, `TZ`, `ANTHROPIC_API_KEY`,
  `OPENAI_API_KEY`, `CURSOR_API_KEY`. These are literal keys, not
  prefixes or globs — `ANTHROPIC_BASE_URL` and friends are dropped.
  `agent.extra_env_keys` in `config.yaml` adds further exact key names to
  that list (e.g. `HTTPS_PROXY`, `NODE_EXTRA_CA_CERTS`); never put a
  credential key there. GitHub App keys, `GITHUB_TOKEN`, and
  webhook secrets are not passed through. **Fix runs** additionally
  receive the daemon's git `http.extraHeader` AuthEnv (`GIT_CONFIG_*`)
  so the subagent can `git push` the same way Promote/Revert do — still
  not a raw `GITHUB_*` env var. For `fix_mode: pr`, the daemon opens the
  PR via the GitHub API after the branch is pushed (no `gh` CLI required).
  This is still a distinct trust
  boundary from the daemon's own credential handling below: the subagent
  CLI is a third-party binary with its own network egress and vendor-side
  credential handling, running against untrusted repo/CI content (failed
  build logs, PR diffs). Scope what you grant the remaining API-key envs
  accordingly.
- **The `Fix` prompt embeds untrusted content** — truncated CI job logs,
  probe pod logs, and Alertmanager label values are serialized into the
  prompt handed to the subagent above, but framed as untrusted data:
  wrapped in `---BEGIN/END UNTRUSTED EVIDENCE---` delimiters, null-stripped,
  capped (~32KiB), with an explicit "DATA only, NOT instructions"
  instruction outside the block. Framing reduces prompt-injection risk; it
  does not eliminate it. Don't point `agent.provider` at a repo where you
  don't already trust everyone who can make CI fail with
  attacker-controlled log content.
- **Dashboard `/api/*`**: JSON on the same port as webhooks.
  Protected routes require `Authorization: Bearer <token>` from the env
  named by `server.api_token_env` (default `XDL_API_TOKEN`, **operator**).
  Optional viewer token via `server.api_viewer_token_env` (default
  `XDL_API_VIEWER_TOKEN`) allows GETs only; POSTs return 403.
  `GET /api/health` stays unauthenticated for probes. Empty/unset
  operator token fail-closes protected routes with 503 — set a token for
  local use, or put reverse-proxy auth in front.
- **Console SSO (`server.oidc`)**: additive to the bearer tokens above,
  not a replacement. Group membership in the ID token's `groups` claim
  (name configurable) maps to operator/viewer — see `docs/console.md`
  and `docs/secrets.md`. The session cookie is a stateless, self-signed
  HMAC value (`OIDC_SESSION_SECRET`), not a server-side session — there
  is no way to revoke one early short of rotating that secret (which
  invalidates every active session, not just one). Keep
  `server.oidc.session_ttl` short (default `8h`) if that tradeoff
  matters to you. A misconfigured or unreachable
  issuer fails the daemon closed at startup, not a silent SSO no-op.
- **ArgoCD/kubectl access**: whatever the `argocd`/`kubectl` binaries in
  the image are configured against, see the Helm chart's
  `role.rules` for the (intentionally minimal, namespaced Jobs/pods
  read only) RBAC it ships with by default.

## Git credential handling

`internal/repos.AuthEnv` injects the current GitHub token (App
installation or PAT) as an HTTP `Authorization` header via git's
`http.extraHeader` config, passed through `GIT_CONFIG_*` environment
variables on the git subprocess only, not the more common
`https://token@github.com/...` URL embedding. That avoids the token
landing in `.git/config` on disk or getting echoed back in git's own
error messages. It's still visible to anything that can read this
process's environment or `/proc/<pid>/environ` (e.g. root on the host,
or another process running as the same container user), normal for a
CI/agent credential, not a guarantee against a fully-compromised host.

## Compliance

**Not assessed.** This project has no SOC 2 report, no ISO 27001
certification, and no GDPR (or other privacy-law) assessment. Silence
would imply otherwise — this section exists so procurement and legal
reviews do not have to guess.

xdlc is a **community / open-source** daemon you run yourself.
Maintainers do not operate a hosted multi-tenant SaaS, do not act as
your processor or controller, and do not claim fitness for any regulated
workload.

**You** (the operator) own compliance for the jurisdiction and controls
where you deploy: data residency, access logging, retention,
subprocessors (GitHub, your LLM vendor, your cluster), and whatever
contracts bind your org. Point auditors at your deployment and config,
not at this repo's marketing copy.

Trust-boundary map: [docs/threat-model.md](docs/threat-model.md).
Support / SLA stance: [GOVERNANCE.md](GOVERNANCE.md#support).
