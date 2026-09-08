#!/usr/bin/env bash
# Regenerates the README hero: a screenshot of a real Fix pull request.
#
#   PR_URL=https://github.com/xdlc-labs/xdlc-agent/pull/51 bash docs/assets/pr.sh
#
# The hero has to be a pull request that xdlc actually opened, not a mockup
# and not the demo GIF -- the GIF fixes a toy `a - b` in a throwaway repo,
# which proves the loop and undersells the product. Open a new one with:
#
#   xdlc fix <run-url-of-a-real-red-run>
#
# then point PR_URL at it. Anonymous Chrome is deliberate: it renders the
# page every reader sees, with no logged-in chrome or merge box.
set -euo pipefail

url="${PR_URL:?set PR_URL to the pull request to shoot}"
out="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/pr.png"
shot="$(mktemp -d)/full.png"
trap 'rm -rf "$(dirname "$shot")"' EXIT

for bin in google-chrome google-chrome-stable chromium; do
  command -v "$bin" >/dev/null 2>&1 && chrome="$bin" && break
done
: "${chrome:?need google-chrome or chromium}"

"$chrome" --headless=new --disable-gpu --no-sandbox --hide-scrollbars \
  --force-color-profile=srgb --window-size=1280,1400 \
  --screenshot="$shot" "$url" >/dev/null 2>&1

# Crop the repo bar through the commit row's green check, dropping the
# site nav above it and the sign-up banner below.
python3 - "$shot" "$out" <<'PY'
import sys
from PIL import Image

src, dst = sys.argv[1], sys.argv[2]
im = Image.open(src)
im.crop((16, 84, 1264, 700)).convert("P", palette=Image.ADAPTIVE, colors=190).save(dst)
print(dst)
PY
