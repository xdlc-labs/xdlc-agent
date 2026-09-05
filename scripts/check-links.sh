#!/usr/bin/env bash
# Verify every relative markdown link and #anchor in the repo's docs
# resolves. External http(s)/mailto links are listed, never fetched.
#
#   ./scripts/check-links.sh            # whole repo
#   ./scripts/check-links.sh README.md  # specific files
#
# Exit 1 on the first broken target. Wired into `make check-links`.
set -uo pipefail

cd "$(dirname "$0")/.."

if [ "$#" -gt 0 ]; then
  candidates=("$@")
else
  mapfile -t candidates < <(git ls-files '*.md' | grep -v '^ui/node_modules/')
fi

# git ls-files still lists files deleted in the working tree; reading those
# would abort awk mid-run. Links *to* a deleted file are still reported as
# broken by the resolver below — this only skips them as link *sources*.
files=()
for f in "${candidates[@]}"; do
  [ -f "$f" ] && files+=("$f")
done

# GitHub's heading -> anchor slug: lowercase, strip anything that is not
# alphanumeric / space / hyphen / underscore, spaces to hyphens.
slug() {
  printf '%s\n' "$1" |
    tr '[:upper:]' '[:lower:]' |
    sed -E 's/`//g; s/[^a-z0-9 _-]//g; s/^ +//; s/ +$//; s/ +/-/g'
}

anchors_of() {
  local f="$1" line text
  [ -f "$f" ] || return 0
  # ATX headings outside fenced code blocks.
  awk '/^```/ {fence = !fence; next} !fence && /^#{1,6} / {print}' "$f" |
    while IFS= read -r line; do
      text="${line#\#}"
      while [ "${text:0:1}" = "#" ]; do text="${text#\#}"; done
      slug "${text# }"
    done
  # Explicit <a name=…> / id=… targets, if any.
  grep -oE '<a[^>]+(name|id)="[^"]+"' "$f" 2>/dev/null |
    sed -E 's/.*"([^"]+)"/\1/' || true
}

fail=0
checked=0

for f in "${files[@]}"; do
  dir="$(dirname "$f")"
  # Inline links: [text](target) -- strip fenced code first.
  while IFS= read -r target; do
    case "$target" in
      http://*|https://*|mailto:*|tel:*) continue ;;
      '') continue ;;
    esac
    checked=$((checked + 1))
    path="${target%%#*}"
    frag="${target#*#}"
    [ "$frag" = "$target" ] && frag=""

    if [ -z "$path" ]; then
      resolved="$f" # same-file #anchor
    elif [ "${path:0:1}" = "/" ]; then
      resolved=".${path}"
    else
      resolved="$dir/$path"
    fi

    if [ ! -e "$resolved" ]; then
      echo "BROKEN PATH  $f -> $target"
      fail=1
      continue
    fi

    if [ -n "$frag" ] && [ "${resolved##*.}" = "md" ]; then
      want="$(slug "$frag")"
      if ! anchors_of "$resolved" | grep -qxF "$want"; then
        echo "BROKEN ANCHOR $f -> $target  (no heading slugging to '#$want' in $resolved)"
        fail=1
      fi
    fi
  done < <(
    awk '/^```/ {fence = !fence; next} !fence' "$f" |
      grep -oE '\]\([^)[:space:]]+\)' |
      sed -E 's/^\]\(//; s/\)$//'
  )
done

if [ "$fail" -eq 0 ]; then
  echo "OK: $checked relative link(s)/anchor(s) resolve across ${#files[@]} file(s)."
else
  echo "FAILED: see broken targets above."
fi
exit "$fail"
