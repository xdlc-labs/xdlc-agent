#!/usr/bin/env bash
# Check local tools before bootstrap. Exits non-zero on first missing dependency.
set -euo pipefail

missing=()
for cmd in git docker kind kubectl helm; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    missing+=("$cmd")
  fi
done

if ((${#missing[@]})); then
  echo "Missing required tools: ${missing[*]}" >&2
  echo "Install: git, docker, kind (>=0.27), kubectl, helm (>=3.14)" >&2
  exit 1
fi

if ! docker info >/dev/null 2>&1; then
  echo "Docker daemon is not running." >&2
  exit 1
fi

echo "Prerequisites OK (git docker kind kubectl helm)"
