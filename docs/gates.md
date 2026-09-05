# Gates

A Gate is anything implementing `internal/gate.Gate`:

```go
type Gate interface {
    Name() string
    Trigger() TriggerKind // OnPush | OnSync | Continuous
    Check(ctx context.Context, repo string) (Result, error)
}
```

## Built-in gates

| Gate | Trigger | Checks | File |
|---|---|---|---|
| `ci` | `on_push` | Latest GitHub Actions `workflow_run` conclusion | `internal/gate/ci.go` |
| `dev-smoke` | `on_sync` | ArgoCD app Synced+Healthy, then smoke/e2e probe Job exit code | `internal/gate/smoke.go` |
| `prod-health` | `continuous` | PromQL p95 latency + error rate vs. configured thresholds | `internal/gate/prodhealth.go` |

Each is a thin struct with injected functions (`GetStatus`, `AppHealthy`,
`ProbeResult`, `Query`) rather than a baked-in client: swap the function
for a fake in tests, or for a different backend (GitLab CI instead of
GitHub Actions, Flux instead of ArgoCD, another PromQL store instead of
Prometheus) without touching the interface.

## Wiring notes (MVP simplifications)

- **CI** is real-time via a GitHub `workflow_run` webhook in the daemon
  (`internal/webhook`): the CI `Gate.Check` implementation itself is
  only used by the `gate check ci` CLI one-shot path, and takes
  `"owner/repo"` (config `github:` field), not the short repo name.
  Evidence passed to Fix includes the run **conclusion + URL**; on Fix the
  daemon also fetches a truncated failed-job log excerpt via the GitHub
  API when possible (`internal/ghclient.FetchFailedJobLogs`).
- **dev-smoke** is poll-driven with an optional ArgoCD notification
  webhook (`/webhooks/argocd`) as the fast path. One `SmokeGate` per
  repo (own ArgoCD Application + probe Job), ticking on
  `gates.dev-smoke.interval`. Emissions are edge-triggered (Kind change
  only).
- **prod-health** is poll-driven with an optional Alertmanager webhook
  (`/webhooks/alertmanager`) as the fast path. Also edge-triggered.
  Queries hit `gates.prod-health.metrics_url` (any PromQL instant-query
  API). Queries may contain `{{repo}}`, replaced with the config short
  name per Check — use that for per-service SLOs. Without the
  placeholder, the same org-wide query runs for every listed repo.
How this lines up with the hero diagram: [architecture.md § vs the diagram](architecture.md#vs-the-diagram).

## Validating config.yaml against gitops/

`config.yaml`'s `argocd_app`/`probe_job` and the ArgoCD Application
manifests under `gitops/apps/dev/` are wired together only by naming
convention: nothing else catches a typo'd `argocd_app` until the
dev-smoke gate mysteriously never passes. Run:

```sh
xdlc-agent validate --config config.yaml --gitops-dir gitops
```

CI runs this against `config.example.yaml` on every push (see
`.github/workflows/ci.yml`) so the example stays honest; run it against
your own `config.yaml` too, ideally in your own CI. See
`internal/validate` for what it checks.

## Fleet policy (depends_on)

Optional fleet-policy knobs (v2) — see `config.example.yaml` `fleet:`,
`repos[].depends_on`, `promote_requires`, `gates.external`, and
`agent.route`. Off by default. When set, the orchestrator may suppress
Fix/Revert/Promote after `Decide` (evidence `escalate=*`).
Details: [architecture.md](architecture.md#fleet-policy),
[runbook.md](runbook.md).

### External gates (v2)

`gates.external` runs an operator-supplied command:

- stdin JSON: `{"repo":"<name>"}`
- stdout JSON: `{"ok":true|false,"evidence":{…}}`

Example: [`scripts/gates/example-external-gate.sh`](../scripts/gates/example-external-gate.sh).
Use this to wrap GitLab CI / Flux checks without forking the binary.

## Adding a gate

1. Implement `Gate` in `internal/gate/yourgate.go`.
2. Add a config section under `gates:` in `config.yaml` (see
   `internal/config/config.go`).
3. Wire it into the daemon's trigger source in `cmd/xdlc-agent`: a
   webhook handler for `OnPush`/`OnSync`, or a ticker for `Continuous`.
4. Decide what `Signal.Kind` a fail/pass produces and whether
   `orchestrator.Decide` needs a new case for it.
