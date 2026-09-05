#!/usr/bin/env bash
# One-time personalization: replace your-org/your-domain placeholders repo-wide.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

default_org="${GITHUB_ORG:-}"
default_repo="${GITHUB_REPO:-xdlc}"
default_domain="${DOMAIN:-xdlc.local}"

if [[ -z "$default_org" ]]; then
  read -rp "GitHub org or username: " default_org
fi
read -rp "GitHub repo name [$default_repo]: " input_repo
default_repo="${input_repo:-$default_repo}"
read -rp "Local ingress domain [$default_domain]: " input_domain
default_domain="${input_domain:-$default_domain}"

REGISTRY="ghcr.io/${default_org}"
REPO_URL="https://github.com/${default_org}/${default_repo}.git"

echo "Personalizing for ${default_org}/${default_repo} (${default_domain})..."

replace_in_files() {
  local from="$1" to="$2"
  find "$ROOT" -type f \
    ! -path "$ROOT/.git/*" \
    ! -path "$ROOT/scripts/setup.sh" \
    \( -name '*.yaml' -o -name '*.yml' -o -name '*.md' -o -name '*.sh' -o -name '*.example' -o -name 'Makefile' \) \
    -print0 | while IFS= read -r -d '' f; do
      if grep -q "$from" "$f" 2>/dev/null; then
        sed -i "s|${from}|${to}|g" "$f"
      fi
    done
}

replace_in_files "your-org" "$default_org"
replace_in_files "your-domain.io" "$default_domain"
replace_in_files "prometheus.your-domain.io" "prometheus.${default_domain}"
replace_in_files "ghcr.io/your-org" "$REGISTRY"

cat > "$ROOT/.env" <<EOF
GITHUB_ORG=${default_org}
GITHUB_REPO=${default_repo}
DOMAIN=${default_domain}
REGISTRY=${REGISTRY}
REPO_URL=${REPO_URL}
CLUSTER_NAME=xdlc
EOF

if [[ ! -f config.yaml ]]; then
  cp config.example.yaml config.yaml
  echo "Created config.yaml from config.example.yaml"
fi

echo ""
echo "Setup complete."
echo "  GitHub:  ${default_org}/${default_repo}"
echo "  Domain:  *.${default_domain}"
echo "  Registry: ${REGISTRY}"
echo ""
echo "Next: ./scripts/bootstrap-local.sh"
echo "      (push this repo to GitHub first if you want ArgoCD GitOps sync)"
