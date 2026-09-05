#!/usr/bin/env bash
# Thin wrapper: check tools, optionally plan, print next steps for AWS EKS.
# Does not apply by default — see infra/aws-eks/README.md.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TF_DIR="${ROOT}/infra/aws-eks"

usage() {
  echo "Usage: $0 [--plan]"
  echo "  --plan   terraform init + plan in infra/aws-eks"
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
need aws
need kubectl
need helm

echo "==> AWS identity"
aws sts get-caller-identity

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
  3. eval "\$(terraform output -raw configure_kubectl)"   # or run the printed aws eks update-kubeconfig
  4. Install ingress + Argo CD, then Helm xdlc — see ${TF_DIR}/README.md
  5. helm upgrade --install xdlc-agent deploy/helm/xdlc-agent \\
       -f deploy/overlays/eks/values.yaml ...

Starter only — not a production multi-AZ hardened reference.
EOF
