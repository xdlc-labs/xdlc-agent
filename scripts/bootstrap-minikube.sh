#!/usr/bin/env bash
# Local Minikube bootstrap: ingress + Prometheus + example-service + xdlc-agent.
# ponytail: reuses same Helm/gitops layout as bootstrap-local.sh; only cluster
# provisioning differs (minikube vs kind).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

SKIP_AGENT=false
SKIP_PROMETHEUS=false
MEMORY="${MINIKUBE_MEMORY:-8192}"
CPUS="${MINIKUBE_CPUS:-2}"
DRIVER="${MINIKUBE_DRIVER:-podman}"
for arg in "$@"; do
  case "$arg" in
    --skip-agent) SKIP_AGENT=true ;;
    --skip-prometheus) SKIP_PROMETHEUS=true ;;
    -h|--help)
      echo "Usage: $0 [--skip-agent] [--skip-prometheus]"
      exit 0
      ;;
  esac
done

# shellcheck source=/dev/null
[[ -f .env ]] && source .env
DOMAIN="${DOMAIN:-xdlc.local}"

command -v minikube >/dev/null || { echo "minikube required"; exit 1; }
command -v kubectl >/dev/null || { echo "kubectl required"; exit 1; }
command -v helm >/dev/null || { echo "helm required"; exit 1; }

# Rootless podman: store images on /var if /home is tight (optional).
if [[ -z "${CONTAINERS_STORAGE_CONF:-}" && -d /var/tmp/xdlc-podman/storage ]]; then
  export CONTAINERS_STORAGE_CONF=/var/tmp/xdlc-storage.conf
fi

if ! minikube status >/dev/null 2>&1; then
  minikube config set rootless true 2>/dev/null || true
  minikube start --driver="$DRIVER" --cpus="$CPUS" --memory="$MEMORY" --container-runtime=containerd
fi

echo "==> Enabling ingress addon..."
minikube addons enable ingress || echo "WARN: ingress addon slow/failed — use port-forward fallback"

kubectl apply -f kubernetes/namespaces/

export PATH="$ROOT/bin:$PATH"
helm repo add argo https://argoproj.github.io/argo-helm >/dev/null 2>&1 || true
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts >/dev/null 2>&1 || true

if ! $SKIP_PROMETHEUS; then
  echo "==> Installing Prometheus (trimmed)..."
  helm upgrade --install kube-prometheus prometheus-community/kube-prometheus-stack \
    --namespace monitoring \
    --create-namespace \
    --set grafana.enabled=false \
    --set alertmanager.enabled=true \
    --set prometheus.prometheusSpec.serviceMonitorSelectorNilUsesHelmValues=false \
    --wait --timeout 10m
fi

PROM_URL="http://kube-prometheus-kube-prome-prometheus.monitoring.svc.cluster.local:9090"

echo "==> Building example-service inside minikube..."
minikube image build -t example-service:local -f services/example-service/Dockerfile services/example-service

echo "==> Deploying example-service..."
helm upgrade --install example-service helm/service-template \
  --namespace dev \
  --create-namespace \
  -f gitops/values/dev.yaml \
  -f gitops/values/dev/example-service.yaml \
  --set image.repository=example-service \
  --set image.tag=local \
  --set image.pullPolicy=IfNotPresent \
  --set nameOverride=example-service \
  --set ingress.enabled=false \
  --set service.type=NodePort

kubectl apply -f observability/probes/smoke-e2e-job.yaml
kubectl apply -f observability/prometheus/rules/prod-health.yaml 2>/dev/null || true

if ! $SKIP_AGENT; then
  AGENT_CONFIG=$(mktemp)
  sed "s|http://prometheus.your-domain.io|${PROM_URL}|g" config.example.yaml > "$AGENT_CONFIG"
  sed -i "s|metrics_url:.*|metrics_url: ${PROM_URL}|" "$AGENT_CONFIG"

  echo "==> Deploying xdlc-agent (published image)..."
  helm upgrade --install xdlc-agent deploy/helm/xdlc-agent \
    --namespace xdlc \
    --create-namespace \
    --set image.repository=ghcr.io/xdlc-labs/xdlc-agent \
    --set image.tag=2.0.0 \
    --set image.pullPolicy=IfNotPresent \
    --set-file config="$AGENT_CONFIG" \
    --set existingSecret=xdlc-agent-secrets

  rm -f "$AGENT_CONFIG"
fi

NODE_PORT=$(kubectl get svc -n dev example-service -o jsonpath='{.spec.ports[0].nodePort}' 2>/dev/null || echo "")
MINIKUBE_IP=$(minikube ip)

cat <<EOF

Minikube stack deployed.

  example-service:  http://${MINIKUBE_IP}:${NODE_PORT}/healthz  (NodePort)
  or:               minikube service -n dev example-service --url
  xdlc-agent:       kubectl port-forward -n xdlc svc/xdlc-agent 8080:8080
  Prometheus:       kubectl port-forward -n monitoring svc/kube-prometheus-kube-prome-prometheus 9090:9090

If ingress addon is healthy: enable ingress in gitops/values/dev.yaml and redeploy.
Rootless podman minikube: if kube-proxy crashes with "too many open files", free /home
disk space or use Docker driver instead: minikube start --driver=docker

EOF
