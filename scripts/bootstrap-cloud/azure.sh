#!/usr/bin/env bash
# Thin wrapper: check tools, optionally plan, print next steps for Azure AKS.
# Does not apply by default — see infra/azure-aks/README.md.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TF_DIR="${ROOT}/infra/azure-aks"

usage() {
  echo "Usage: $0 [--plan]"
  echo "  --plan   terraform init + plan in infra/azure-aks"
  echo "  (default) check prereqs and print next steps"
}

DO_PLAN=false
for arg in "$@"; do
  case "$arg" in
    --plan) DO_PLAN=true ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown arg: $arg"; usage; exit 1 ;;
  esac
done

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing: $1 — install it, then re-run."
    exit 1
  }
}

need terraform
need az
need kubectl
need helm

echo "==> az identity"
az account show --query "{subscription:name, id:id}" -o table

if [[ "$DO_PLAN" == true ]]; then
  echo "==> terraform init + plan (${TF_DIR})"
  (
    cd "$TF_DIR"
    terraform init
    terraform plan
  )
fi

cat <<EOF

Next steps:
  1. cd ${TF_DIR}
  2. terraform init && terraform apply
  3. eval "\$(terraform output -raw configure_kubectl)"   # or run the printed az aks command
  4. Install ingress + Argo CD, then Helm xdlc — see ${TF_DIR}/README.md
  5. helm upgrade --install xdlc-agent deploy/helm/xdlc-agent \\
       -f deploy/overlays/aks/values.yaml ...

Starter only — not a production multi-zone hardened reference.
EOF
