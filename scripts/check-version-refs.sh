#!/usr/bin/env bash
# Every copy-pasteable release reference in the docs must match the Helm
# chart's appVersion, which is what a release bump edits. A stale image tag or
# XDLC_VERSION in an install snippet is a first-run failure for a new user and
# is easy to miss in review, so CI checks it instead of a human.
#
# Only release-shaped references are matched (an image tag, a Helm image.tag, an
# XDLC_VERSION pin, the chart's own version fields) so that IP addresses, Go
# versions and action pins do not trip it.
set -euo pipefail
cd "$(dirname "$0")/.."

want="$(sed -n 's/^appVersion: *"\(.*\)"$/\1/p' deploy/helm/xdlc-agent/Chart.yaml)"
if [ -z "$want" ]; then
  echo "check-version-refs: cannot read appVersion from deploy/helm/xdlc-agent/Chart.yaml" >&2
  exit 1
fi

# CHANGELOG.md is excluded on purpose: its entries must keep naming the version
# they actually shipped in. Hosted install / getting-started / deployment pins
# live in the documentation repository, not this one.
files=(
  README.md
  deploy/helm/xdlc-agent/Chart.yaml
)

# Prints "line:version" for each release-shaped reference. POSIX ERE via
# grep -o and sed only: macOS grep has no PCRE, minimal hosts have no perl, and
# RELEASING.md runs this locally on both.
semver='[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?'
scan_refs() {
  grep -noE "(xdlc-agent:|image\.tag=|XDLC_VERSION=v|^appVersion: \"|^version: )${semver}" "$1" \
    | sed -E "s/^([0-9]+):.*[:=v\" ](${semver})\$/\1:\2/"
}

status=0
found_any=0
for f in "${files[@]}"; do
  if [ ! -f "$f" ]; then
    if git ls-files --error-unmatch "$f" >/dev/null 2>&1; then
      echo "check-version-refs: tracked file missing from the working tree: $f" >&2
      status=1
    else
      echo "check-version-refs: skipping $f (not tracked in this repository)"
    fi
    continue
  fi
  while IFS=: read -r line found; do
    found_any=1
    [ "$found" = "$want" ] && continue
    echo "$f:$line: release reference '$found' != chart appVersion '$want'" >&2
    status=1
  done < <(scan_refs "$f")
done

if [ "$found_any" -eq 0 ]; then
  echo "check-version-refs: matched no release references at all — the pattern has rotted" >&2
  exit 1
fi

if [ "$status" -ne 0 ]; then
  echo >&2
  echo "Bump every reference together, or edit the file list in $0." >&2
  exit 1
fi
echo "check-version-refs: all release references match $want"
