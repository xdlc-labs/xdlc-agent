#!/usr/bin/env bash
# Local loopback e2e for the CI → Fix → DEV smoke → Promote → Revert loop.
#
# Preconditions (cluster path, WITH_CLUSTER=1):
#   - minikube (or equivalent) with ArgoCD
#   - Application `dev-example-service` Synced+Healthy
#   - Job `smoke-e2e` in namespace `dev` (complete / passing)
#   - `kubectl` and `argocd` on PATH (`argocd login --core`)
# This script does not install Minikube or ArgoCD.
#
# Usage:
#   scripts/e2e-local.sh              # git + stub agent; Argo only if cluster is up
#   WITH_CLUSTER=1 scripts/e2e-local.sh
#
# Env:
#   XDLC_BIN          default: <repo>/bin/xdlc
#   E2E_DIR           default: /tmp/xdlc-e2e-example-service
#   XDLC_API_TOKEN    default: dev-token (must match the console)
#   WITH_CLUSTER      1 = require real argocd app + probe Job
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
XDLC_BIN="${XDLC_BIN:-$ROOT/bin/xdlc}"
E2E_DIR="${E2E_DIR:-/tmp/xdlc-e2e-example-service}"
TOKEN="${XDLC_API_TOKEN:-dev-token}"
BASE="http://127.0.0.1:18080"
WITH_CLUSTER="${WITH_CLUSTER:-0}"

die() { printf 'e2e-local.sh: %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "need $1 on PATH"; }

need git
need python3
need curl
[ -x "$XDLC_BIN" ] || die "missing $XDLC_BIN — run: make build"

export XDLC_API_TOKEN="$TOKEN"
export OTEL_SDK_DISABLED=true
unset OTEL_EXPORTER_OTLP_ENDPOINT || true

mkdir -p "$E2E_DIR/bin"

# Stub coding agent: commit + push a FIXLOG line on develop (instant).
cat >"$E2E_DIR/bin/claude" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
# stdin is the prompt; ignore it.
cat >/dev/null || true
git add -A
if git diff --cached --quiet && git diff --quiet; then
  echo "stub: nothing to commit" >>FIXLOG.md
  git add FIXLOG.md
fi
git commit -m "fix: example-service (stub agent)" --allow-empty >/dev/null
git push origin HEAD >/dev/null
printf '%s\n' '{"type":"result","result":"ok"}'
EOF
chmod +x "$E2E_DIR/bin/claude"

if [ ! -d "$E2E_DIR/origin.git" ]; then
  git init --bare "$E2E_DIR/origin.git" >/dev/null
  git clone "$E2E_DIR/origin.git" "$E2E_DIR/repo" >/dev/null
  git -C "$E2E_DIR/repo" config user.email "xdlc-e2e@local"
  git -C "$E2E_DIR/repo" config user.name "xdlc-e2e"
  git -C "$E2E_DIR/repo" checkout -b main
  git -C "$E2E_DIR/repo" commit --allow-empty -m "init"
  mkdir -p "$E2E_DIR/repo/gitops/values/dev" "$E2E_DIR/repo/gitops/values/prod"
  printf 'image:\n  tag: sha-dev-abc123\n' >"$E2E_DIR/repo/gitops/values/dev/example-service.yaml"
  printf 'image:\n  tag: sha-prod-old456\n' >"$E2E_DIR/repo/gitops/values/prod/example-service.yaml"
  git -C "$E2E_DIR/repo" add gitops
  git -C "$E2E_DIR/repo" commit -m "gitops values"
  git -C "$E2E_DIR/repo" checkout -b develop
  echo "seed" >"$E2E_DIR/repo/FIXLOG.md"
  git -C "$E2E_DIR/repo" add FIXLOG.md
  git -C "$E2E_DIR/repo" commit -m "develop ahead of main"
  git -C "$E2E_DIR/repo" push -u origin main develop >/dev/null
fi

cat >"$E2E_DIR/config.yaml" <<EOF
repos:
  - name: example-service
    github: xdlc-labs/example-service
    dir: $E2E_DIR/repo
    gates: [ci, dev-smoke, prod-health]
    argocd_app: dev-example-service
    probe_job: smoke-e2e

server:
  addr: "127.0.0.1:18080"
  require_webhook_secret: false

gates:
  ci:
    trigger: on_push
  dev-smoke:
    trigger: on_sync
    namespace: dev
    interval: 24h
  prod-health:
    trigger: continuous
    metrics_url: http://127.0.0.1:9
    thresholds:
      p95_ms: 500
      error_rate: 0.01
    interval: 24h
    p95_query: vector(0)
    error_rate_query: vector(0)

agent:
  mode: subprocess
  provider: claude
  binary: $E2E_DIR/bin/claude
  timeout: 10m
  fix_reverify: false
  ci_rerun_before_fix: false
EOF

cluster_ok=0
if [ "$WITH_CLUSTER" = "1" ]; then
  need kubectl
  need argocd
  kubectl -n argocd get app dev-example-service >/dev/null 2>&1 \
    || die "WITH_CLUSTER=1 but Application dev-example-service is missing"
  kubectl -n dev get job smoke-e2e >/dev/null 2>&1 \
    || die "WITH_CLUSTER=1 but Job smoke-e2e in ns dev is missing"
  cluster_ok=1
elif command -v kubectl >/dev/null 2>&1 && command -v argocd >/dev/null 2>&1; then
  if kubectl -n argocd get app dev-example-service >/dev/null 2>&1 \
    && kubectl -n dev get job smoke-e2e >/dev/null 2>&1; then
    cluster_ok=1
    echo "e2e-local.sh: found Argo app + probe Job; using real cluster"
  fi
fi

if [ "$cluster_ok" != "1" ]; then
  echo "e2e-local.sh: no cluster — Argo Promote uses stub kubectl/argocd"
  mkdir -p "$E2E_DIR/bin"
  cat >"$E2E_DIR/bin/argocd" <<'EOF'
#!/usr/bin/env bash
if [ "${1:-}" = "app" ] && [ "${2:-}" = "get" ]; then
  printf '%s\n' '{"status":{"sync":{"status":"Synced"},"health":{"status":"Healthy"}}}'
  exit 0
fi
exit 0
EOF
  cat >"$E2E_DIR/bin/kubectl" <<'EOF'
#!/usr/bin/env bash
# Job succeeded
if [[ "$*" == *jsonpath=\{.status.succeeded\}* ]]; then
  echo 1
  exit 0
fi
if [[ "$*" == *logs* ]]; then
  echo "smoke-e2e: 3 checks passed"
  exit 0
fi
exit 0
EOF
  chmod +x "$E2E_DIR/bin/argocd" "$E2E_DIR/bin/kubectl"
  export PATH="$E2E_DIR/bin:$PATH"
fi

"$XDLC_BIN" doctor --config "$E2E_DIR/config.yaml" --skip-network || true

# Stop a leftover daemon on this port.
if curl -sS -o /dev/null --max-time 1 "$BASE/api/health" 2>/dev/null; then
  echo "e2e-local.sh: something already listens on 18080 — posting to it"
else
  "$XDLC_BIN" daemon --config "$E2E_DIR/config.yaml" &
  daemon_pid=$!
  for i in $(seq 1 50); do
    curl -sS -o /dev/null --max-time 1 "$BASE/api/health" && break
    sleep 0.1
  done
  curl -sS -o /dev/null --max-time 1 "$BASE/api/health" || die "daemon did not start"
  echo "e2e-local.sh: daemon pid=$daemon_pid (leave running for the console; kill to stop)"
fi

max_seq() {
  python3 - "$BASE" "$TOKEN" <<'PY'
import json,sys,urllib.request
base, token = sys.argv[1], sys.argv[2]
req = urllib.request.Request(base + "/api/history?repo=example-service&limit=20",
                             headers={"Authorization": "Bearer " + token})
with urllib.request.urlopen(req) as r:
    evs = json.load(r).get("events") or []
print(max((e.get("seq") or 0) for e in evs) if evs else 0)
PY
}

wait_event() {
  local want_action="$1" want_source="$2" min_seq="$3" timeout="${4:-45}"
  python3 - "$want_action" "$want_source" "$min_seq" "$timeout" "$BASE" "$TOKEN" <<'PY'
import json, sys, time, urllib.request
want_action, want_source, min_seq, timeout, base, token = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4]), sys.argv[5], sys.argv[6]
deadline = time.time() + timeout
last = None
while time.time() < deadline:
    req = urllib.request.Request(base + "/api/history?repo=example-service&limit=20",
                                 headers={"Authorization": "Bearer " + token})
    with urllib.request.urlopen(req) as r:
        evs = json.load(r).get("events") or []
    last = evs[0] if evs else None
    for e in evs:
        seq = e.get("seq") or 0
        action = (e.get("action") or "").lower()
        source = (e.get("source") or "").lower()
        if seq > min_seq and action == want_action.lower() and want_source.lower() in source:
            print(json.dumps({"ok": True, "seq": seq, "source": e.get("source"), "action": e.get("action")}))
            sys.exit(0)
    time.sleep(0.4)
print(json.dumps({"ok": False, "last": last}))
sys.exit(1)
PY
}

echo "======== unknown Argo app → 204 ========"
code=$(curl -sS -o /tmp/xdlc-e2e-unknown.txt -w "%{http_code}" -X POST "$BASE/webhooks/argocd" \
  -H "Content-Type: application/json" \
  -d '{"app":"not-mine","sync":"Synced","health":"Healthy"}')
[ "$code" = "204" ] || die "unknown app status=$code want 204"

echo "======== GitHub CI fail → Fix ========"
SEQ=$(max_seq)
SHA=$(git -C "$E2E_DIR/origin.git" rev-parse develop)
code=$(curl -sS -o /tmp/xdlc-e2e-gh.txt -w "%{http_code}" -X POST "$BASE/webhooks/github" \
  -H "Content-Type: application/json" \
  -H "X-GitHub-Event: workflow_run" \
  -H "X-GitHub-Delivery: e2e-local-$(date +%s)" \
  -d "$(python3 - "$SHA" <<'PY'
import json,sys
sha=sys.argv[1]
print(json.dumps({
  "action":"completed",
  "repository":{"full_name":"xdlc-labs/example-service"},
  "workflow_run":{
    "event":"push","conclusion":"failure","head_branch":"develop","head_sha":sha,
    "head_repository":{"full_name":"xdlc-labs/example-service"},
    "html_url":"https://github.com/xdlc-labs/example-service/actions/runs/1"
  }
}))
PY
)")
[ "$code" = "202" ] || die "github status=$code want 202"
wait_event Fix github "$SEQ" 30

echo "======== Argo DEV smoke pass → Promote ========"
SEQ=$(max_seq)
code=$(curl -sS -o /tmp/xdlc-e2e-argo.txt -w "%{http_code}" --max-time 90 -X POST "$BASE/webhooks/argocd" \
  -H "Content-Type: application/json" \
  -d '{"app":"dev-example-service","sync":"Synced","health":"Healthy"}')
[ "$code" = "202" ] || die "argocd status=$code want 202"
wait_event Promote argocd "$SEQ" 45

echo "======== Alertmanager firing → Revert ========"
SEQ=$(max_seq)
code=$(curl -sS -o /tmp/xdlc-e2e-am.txt -w "%{http_code}" -X POST "$BASE/webhooks/alertmanager" \
  -H "Content-Type: application/json" \
  -d '{"status":"firing","alerts":[{"status":"firing","labels":{"repo":"example-service","alertname":"HighP95Latency"}}]}')
[ "$code" = "202" ] || die "alertmanager status=$code want 202"
wait_event Revert prometheus "$SEQ" 30

if [ "$cluster_ok" = "1" ]; then
  echo "======== restore Argo automated sync (selfHeal) ========"
  kubectl -n argocd patch application dev-example-service --type merge \
    -p '{"spec":{"syncPolicy":{"automated":{"prune":true,"selfHeal":true}}}}' >/dev/null || true
fi

echo "e2e-local.sh: ok  (console http://127.0.0.1:18080/  token=$TOKEN)"
