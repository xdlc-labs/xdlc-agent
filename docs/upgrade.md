# Upgrade / migration

Version-to-version notes for **`xdlc-agent`** config and Helm values.
Release details live in [CHANGELOG.md](../CHANGELOG.md) — this page is
the migration checklist, not a duplicate changelog.

Always run before cutting over:

```sh
xdlc-agent validate --config config.yaml --gitops-dir gitops
```

## Compatibility (SemVer)

Intent, not a legal SLA. Current line is **2.x**.

| Bump | When |
|---|---|
| **MAJOR** | Breaking config schema, `/api/*` contract, or operator CLI that scripts depend on |
| **MINOR** | Backward-compatible features (optional config keys, new API fields/endpoints, gates/providers) |
| **PATCH** | Fixes, docs, non-behavioral refactors |

**Config:** additive defaults OK in minor/patch. Renames = major, or a
documented temporary alias (e.g. `prometheus_url` → `metrics_url`).
Validation that rejects previously accepted values is breaking — call
it out in CHANGELOG.

**`/api/*`:** paths/fields in [api-reference.md](api-reference.md).
Additive JSON OK; removing/renaming fields = major. Error bodies today
are plain `http.Error` text — not a stable JSON schema. `GET /api/health`
stays unauthenticated.

**Helm chart:** breaking `values.yaml` renames = chart major; new
optional values = minor. `appVersion` should track the tested
`xdlc-agent` release. Image tags: pin `image.tag` (or digest); `latest`
is for demos.

**Not promised:** HA / multi-replica until storage + election exist
([disaster-recovery.md](disaster-recovery.md)); third-party subagent CLI
flag/stdout shapes outside our SemVer; `helm/service-template` is a
separate chart with the same values-breaking = major idea.

Drift without a CHANGELOG / upgrade note → bug (or [SECURITY.md](../SECURITY.md)
if security-sensitive).

## Already shipped (pre-0.1.x → current)

These renames landed in the 0.1 line. Update configs if you still use
the old keys.

### `claude:` → `agent:`

The coding-agent block is now `agent:` (provider-pluggable: `claude` /
`codex` / `cursor`).

```yaml
# before
claude:
  timeout: 10m

# after
agent:
  mode: subprocess
  provider: claude
  timeout: 10m
```

`agent.binary` / `agent.args` still override the default CLI invocation.
See [architecture.md](architecture.md).

### `prometheus_url` → `metrics_url`

Under `gates.prod-health`:

```yaml
# preferred
metrics_url: http://prometheus.monitoring.svc:9090

# legacy alias — still accepted when metrics_url is empty; prefer metrics_url
# prometheus_url: http://prometheus.monitoring.svc:9090
```

Any PromQL instant-query API works (Prometheus, VictoriaMetrics,
OpenObserve, Mimir). See [deployment.md](deployment.md#metrics-backends-promql).

## Breaking / required changes

Apply these when moving onto a build that includes the hardening fixes
for auth, branch config, RBAC, and config validation.

### `/api/*` requires a token

Protected dashboard routes fail closed without an operator token:

| Config | Default env | Role |
|---|---|---|
| `server.api_token_env` | `XDL_API_TOKEN` | operator (GET + POST) |
| `server.api_viewer_token_env` | `XDL_API_VIEWER_TOKEN` | optional viewer (GET only) |

Unset / empty operator token → **503** `API token not configured` on
everything except `GET /api/health`. Set the secret in the same place
as other daemon env (Helm `existingSecret`, local `.env`). Details:
[api-reference.md](api-reference.md), [console.md](console.md),
[secrets.md](secrets.md).

### `prod_branch` (per repo)

Promote / revert no longer hardcode `main`. Optional per-repo field:

```yaml
repos:
  - name: my-service
    github: your-org/my-service
    branch: develop       # Fix / CI target (default develop)
    prod_branch: main     # Promote / Revert target (default main)
```

Orgs that use `release` → `production` (or similar) must set both.
Default remains `develop` → `main` if omitted.

### Helm `clusterRole` → `role`

Chart RBAC is a namespaced `Role` + `RoleBinding`, not a
`ClusterRole`. Values key:

```yaml
# before (removed)
# clusterRole:
#   create: true
#   rules: [...]

# after
role:
  create: true
  namespace: dev   # smoke probe namespace; may differ from release ns
  rules:
    - apiGroups: ["batch"]
      resources: ["jobs"]
      verbs: ["get", "list"]
    - apiGroups: [""]
      resources: ["pods", "pods/log"]
      verbs: ["get", "list"]
```

If your overlay still sets `clusterRole`, rename to `role` and set
`role.namespace` to the namespace where probe Jobs run.

### `agent.mode: sdk` rejected

`xdlc-agent validate` (and daemon load) reject `agent.mode: sdk` —
unimplemented, not silently ignored. Use `subprocess` or omit `mode`
(defaults to subprocess). SDK mode stays reserved for a future release;
see [CHANGELOG.md](../CHANGELOG.md) / [architecture.md](architecture.md).

## Since 0.1.2

Additive / optional — default behavior unchanged unless you opt in.

### Optional OIDC (`server.oidc`)

SSO for the console is additive to bearer tokens. Empty / omitted
`issuer_url` → OIDC disabled (bearer-only, same as before). When
`issuer_url` is set, discovery failure is **fatal at startup** (no silent
fallback), and `OIDC_CLIENT_SECRET` + `OIDC_SESSION_SECRET` (or the env
names you configure) are required. Group → role mapping:
`operator_groups` / `viewer_groups`. Session cookies last
`session_ttl` (default `8h`) and carry `Secure` unless you set
`cookie_secure: false`. See [console.md](console.md) and
[secrets.md](secrets.md); example block in `config.example.yaml`.

### `agent.fix_mode: direct|pr`

```yaml
agent:
  fix_mode: direct   # default — commit+push to the watched branch
  # fix_mode: pr     # scratch branch + open PR; Fix returns when PR exists
```

Default remains `direct`. Switching to `pr` changes Fix evidence
(`pr_number` / `pr_url` / …) and enables the console Fix-PR queue
(`GET /api/prs`). No forced migration.

### Helm `backup.*` (opt-in)

PVC backup templates ship disabled:

```yaml
backup:
  velero:
    enabled: false
    # schedule, veleroNamespace, ttl …
  csiSnapshot:
    enabled: false
    # schedule, image, volumeSnapshotClassName …
```

Enable only after Velero or a VolumeSnapshotClass exists in the cluster.
Procedure and RPO/RTO: [disaster-recovery.md](disaster-recovery.md).

### Cosign / SBOM on release images

Tag releases publish an SPDX SBOM asset and keyless cosign signatures.
Verify with the command in [deployment.md](deployment.md#docker-image).
No config migration for running daemons — supply-chain only.

### Fleet policy (`depends_on` / `fleet:`)

Optional. Defaults keep independent-repo behavior.

```yaml
repos:
  - name: web
    depends_on: [api]
    promote_requires:
      - repo: api
        min_tag: "v1.2.0"

fleet:
  flap_max_cycles: 3
  flap_window: 2h
  circuit_breach_ratio: 0.3
  # notify_webhook_url: https://hooks.example/…

gates:
  external:
    - name: example
      command: ["./scripts/gates/example-external-gate.sh"]
      trigger: continuous

agent:
  provider: claude
  providers: [claude, codex]
  route: cheapest   # or static
```

See [architecture.md](architecture.md#fleet-policy) and
[runbook.md](runbook.md).

### New read APIs

`GET /api/whoami`, `GET /api/prs`, `GET /api/kpis` — schemas in
[api-reference.md](api-reference.md). Clients that ignore unknown
endpoints need no change.

## Suggested cutover order

1. Diff your `config.yaml` against `config.example.yaml` for the renames
   above.
2. Add `XDL_API_TOKEN` (and optional viewer token) to the Secret / env.
3. Optionally enable `server.oidc` + OIDC secrets, or leave bearer-only.
4. Set `prod_branch` / `branch` if not `main` / `develop`.
5. Decide `agent.fix_mode` (`direct` default; `pr` for review-gated Fixes).
6. Update Helm values: `role` (not `clusterRole`), pin `image.tag` to the
   release you intend; leave `backup.*` off unless DR is wired.
7. `xdlc-agent validate`, then `helm upgrade`.
8. Smoke: `GET /api/health` (open), then a bearer (or SSO) GET on
   `/api/overview` and `/api/whoami`.

## Where to look next

| Topic | Doc |
|---|---|
| What changed in a given tag | [CHANGELOG.md](../CHANGELOG.md) |
| Compatibility (this page) | [§ Compatibility](#compatibility-semver) |
| API shapes after auth | [api-reference.md](api-reference.md) |
| PVC / audit restore | [disaster-recovery.md](disaster-recovery.md) |
