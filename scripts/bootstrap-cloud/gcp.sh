#!/usr/bin/env bash
# Thin wrapper: check tools, optionally plan, print next steps for GCP GKE.
# Does not apply by default — see infra/gcp-gke/README.md.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TF_DIR="${ROOT}/infra/gcp-gke"

usage() {
  echo "Usage: $0 --project <project-id> [--plan]"
  echo "  --project <id>  GCP project ID (required)"
  echo "  --plan          terraform init + plan in infra/gcp-gke"
  echo "  (default) check prereqs and print next steps"
}

DO_PLAN=false
PROJECT_ID=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --plan) DO_PLAN=true; shift ;;
    --project) PROJECT_ID="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown arg: $1"; usage; exit 1 ;;
  esac
done

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing: $1 — install it, then re-run."
    exit 1
  }
}

need terraform
need gcloud
need kubectl
need helm

echo "==> gcloud identity"
gcloud auth list --filter=status:ACTIVE --format="value(account)"

if [[ "$DO_PLAN" == true ]]; then
  if [[ -z "$PROJECT_ID" ]]; then
    echo "--plan requires --project <project-id>"
    exit 1
  fi
  echo "==> terraform init + plan (${TF_DIR})"
  (
    cd "$TF_DIR"
    terraform init
    terraform plan -var="project_id=${PROJECT_ID}"
  )
fi

cat <<EOF

Next steps:
  1. cd ${TF_DIR}
  2. terraform init && terraform apply -var="project_id=<your-project-id>"
  3. eval "\$(terraform output -raw configure_kubectl)"   # or run the printed gcloud command
  4. Install ingress + Argo CD, then Helm xdlc — see ${TF_DIR}/README.md
  5. helm upgrade --install xdlc-agent deploy/helm/xdlc-agent \\
       -f deploy/overlays/gke/values.yaml ...

Starter only — not a production multi-zone hardened reference.
EOF
