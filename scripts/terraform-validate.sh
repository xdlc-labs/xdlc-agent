#!/usr/bin/env bash
# Validate cloud starter Terraform without a backend or cloud credentials.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
stacks=(infra/aws-eks infra/gcp-gke infra/azure-aks)
for d in "${stacks[@]}"; do
  echo "==> terraform validate ${d}"
  (
    cd "${ROOT}/${d}"
    terraform init -backend=false -input=false
    terraform validate
  )
done
echo "OK: all stacks validate"
