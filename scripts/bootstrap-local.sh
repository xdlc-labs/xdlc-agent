#!/usr/bin/env bash
# Local Kind bootstrap: cluster + ingress + ArgoCD + Prometheus + OTel + example-service + xdlc-agent.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

SKIP_AGENT=false
WITH_GITOPS=false
for arg in "$@"; do
  case "$arg" in
    --skip-agent) SKIP_AGENT=true ;;
    --with-gitops) WITH_GITOPS=true ;;
    -h|--help)
      echo "Usage: $0 [--skip-agent] [--with-gitops]"
      echo "  --skip-agent   Install infra only, skip xdlc-agent Helm release"
      echo "  --with-gitops  Apply gitops/root-*.yaml (repo must exist on GitHub)"
      exit 0
      ;;
  esac
done

# shellcheck source=/dev/null
[[ -f .env ]] && source .env
CLUSTER_NAME="${CLUSTER_NAME:-xdlc}"
DOMAIN="${DOMAIN:-xdlc.local}"
REGISTRY="${REGISTRY:-ghcr.io/your-org}"

"${ROOT}/scripts/verify-prereqs.sh"

echo "==> Creating Kind cluster (${CLUSTER_NAME})..."
if kind get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"; then
  echo "Cluster $CLUSTER_NAME already exists — reusing."
else
  kind create cluster --name "$CLUSTER_NAME" --config local/kind-config.yaml
fi

# ingress-nginx archived Mar 2026 — Contour keeps Ingress API, still maintained.
echo "==> Installing Contour ingress..."
kubectl apply -f https://projectcontour.io/quickstart/contour.yaml
kubectl rollout status -n projectcontour deployment/contour --timeout=180s
kubectl rollout status -n projectcontour daemonset/envoy --timeout=180s

echo "==> Creating namespaces..."
kubectl apply -f kubernetes/namespaces/

echo "==> Installing ArgoCD..."
kubectl create namespace argocd --dry-run=client -o yaml | kubectl apply -f -
helm repo add argo https://argoproj.github.io/argo-helm >/dev/null 2>&1 || true
helm upgrade --install argocd argo/argo-cd \
  --namespace argocd \
  --version 10.7.0 \
  --set server.service.type=ClusterIP \
  --wait --timeout 10m

echo "==> Installing Prometheus (kube-prometheus-stack, trimmed)..."
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts >/dev/null 2>&1 || true
helm upgrade --install kube-prometheus prometheus-community/kube-prometheus-stack \
  --namespace monitoring \
  --create-namespace \
  --set grafana.enabled=false \
  --set alertmanager.enabled=true \
  --set prometheus.prometheusSpec.serviceMonitorSelectorNilUsesHelmValues=false \
  --set prometheus.prometheusSpec.enableRemoteWriteReceiver=true \
  --wait --timeout 10m

PROM_URL="http://kube-prometheus-kube-prome-prometheus.monitoring.svc.cluster.local:9090"

echo "==> Installing OTel Collector..."
kubectl apply -f observability/otel/collector.yaml

echo "==> Building example-service image..."
docker build -t "example-service:local" services/example-service
kind load docker-image "example-service:local" --name "$CLUSTER_NAME"

echo "==> Deploying example-service to dev..."
helm upgrade --install example-service helm/service-template \
  --namespace dev \
  --create-namespace \
  -f gitops/values/dev.yaml \
  -f gitops/values/dev/example-service.yaml \
  --set image.repository=example-service \
  --set image.tag=local \
  --set nameOverride=example-service \
  --set ingress.host="example-service.${DOMAIN}"

kubectl apply -f observability/probes/smoke-e2e-job.yaml
kubectl apply -f observability/prometheus/rules/prod-health.yaml

if $WITH_GITOPS; then
  echo "==> Applying GitOps app-of-apps (requires repo on GitHub)..."
  kubectl apply -f gitops/root-dev.yaml -f gitops/root-prod.yaml
fi

if ! $SKIP_AGENT; then
  echo "==> Building xdlc-agent image..."
  docker build -t "xdlc-agent:local" -f deploy/Dockerfile .
  kind load docker-image "xdlc-agent:local" --name "$CLUSTER_NAME"

  AGENT_CONFIG=$(mktemp)
  sed "s|http://prometheus.your-domain.io|${PROM_URL}|g" config.example.yaml > "$AGENT_CONFIG"
  sed -i "s|metrics_url:.*|metrics_url: ${PROM_URL}|" "$AGENT_CONFIG"
  sed -i "s|prometheus_url:.*|metrics_url: ${PROM_URL}|" "$AGENT_CONFIG" || true
  if [[ -n "${AGENT_PROVIDER:-}" ]]; then
    sed -i "s|provider: claude.*|provider: ${AGENT_PROVIDER}|" "$AGENT_CONFIG"
  fi

  # Secret literals: GitHub App preferred, GITHUB_TOKEN as PAT fallback,
  # GITHUB_WEBHOOK_SECRET, plus whichever coding-agent key is set in .env.
  SECRET_ARGS=(
    --from-literal=GITHUB_TOKEN="${GITHUB_TOKEN:-}"
    --from-literal=GITHUB_WEBHOOK_SECRET="${GITHUB_WEBHOOK_SECRET:-}"
  )
  [[ -n "${GITHUB_APP_ID:-}" ]] && SECRET_ARGS+=(--from-literal=GITHUB_APP_ID="${GITHUB_APP_ID}")
  [[ -n "${GITHUB_APP_INSTALLATION_ID:-}" ]] && SECRET_ARGS+=(--from-literal=GITHUB_APP_INSTALLATION_ID="${GITHUB_APP_INSTALLATION_ID}")
  if [[ -n "${GITHUB_APP_PRIVATE_KEY_FILE:-}" && -f "${GITHUB_APP_PRIVATE_KEY_FILE}" ]]; then
    SECRET_ARGS+=(--from-file=GITHUB_APP_PRIVATE_KEY="${GITHUB_APP_PRIVATE_KEY_FILE}")
  elif [[ -n "${GITHUB_APP_PRIVATE_KEY:-}" ]]; then
    SECRET_ARGS+=(--from-literal=GITHUB_APP_PRIVATE_KEY="${GITHUB_APP_PRIVATE_KEY}")
  fi
  [[ -n "${ARGOCD_WEBHOOK_SECRET:-}" ]] && SECRET_ARGS+=(--from-literal=ARGOCD_WEBHOOK_SECRET="${ARGOCD_WEBHOOK_SECRET}")
  [[ -n "${ALERTMANAGER_WEBHOOK_SECRET:-}" ]] && SECRET_ARGS+=(--from-literal=ALERTMANAGER_WEBHOOK_SECRET="${ALERTMANAGER_WEBHOOK_SECRET}")
  [[ -n "${ANTHROPIC_API_KEY:-}" ]] && SECRET_ARGS+=(--from-literal=ANTHROPIC_API_KEY="${ANTHROPIC_API_KEY}")
  [[ -n "${OPENAI_API_KEY:-}" ]] && SECRET_ARGS+=(--from-literal=OPENAI_API_KEY="${OPENAI_API_KEY}")
  [[ -n "${CURSOR_API_KEY:-}" ]] && SECRET_ARGS+=(--from-literal=CURSOR_API_KEY="${CURSOR_API_KEY}")

  kubectl create namespace xdlc --dry-run=client -o yaml | kubectl apply -f -
  kubectl create secret generic xdlc-agent-secrets --namespace xdlc \
    --dry-run=client -o yaml "${SECRET_ARGS[@]}" | kubectl apply -f -

  helm upgrade --install xdlc-agent deploy/helm/xdlc-agent \
    --namespace xdlc \
    --create-namespace \
    --set image.repository=xdlc-agent \
    --set image.tag=local \
    --set image.pullPolicy=Never \
    --set-file config="$AGENT_CONFIG" \
    --set existingSecret=xdlc-agent-secrets

  rm -f "$AGENT_CONFIG"
fi

HOSTS_LINE="127.0.0.1 example-service.${DOMAIN} argocd.${DOMAIN}"
if ! grep -q "example-service.${DOMAIN}" /etc/hosts 2>/dev/null; then
  echo ""
  echo "Add to /etc/hosts (may need sudo):"
  echo "  ${HOSTS_LINE}"
fi

cat <<EOF

Local stack is up.

  example-service:  http://example-service.${DOMAIN}/healthz
  ArgoCD UI:        kubectl port-forward svc/argocd-server -n argocd 8081:443
                    (admin password: kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d)
  Prometheus:       kubectl port-forward svc/kube-prometheus-kube-prome-prometheus -n monitoring 9090:9090
  OTel Collector:   kubectl -n monitoring get svc otel-collector

GitHub webhook (for CI gate):
  1. Expose xdlc-agent: kubectl port-forward svc/xdlc-agent -n xdlc 8080:8080
  2. Tunnel it: ngrok http 8080 (or cloudflared) → http://localhost:8080
  3. Add webhook: https://<tunnel>/webhooks/github  (workflow_run events, develop branch)
  Also: /webhooks/argocd (dev-smoke), /webhooks/alertmanager (prod-health)

Required env for agent fixes: GITHUB_TOKEN, GITHUB_WEBHOOK_SECRET, and
whichever coding-agent key matches config.yaml's agent.provider
(ANTHROPIC_API_KEY for claude, OPENAI_API_KEY for codex, CURSOR_API_KEY
for cursor)
See docs/getting-started.md for the full walkthrough.

EOF
