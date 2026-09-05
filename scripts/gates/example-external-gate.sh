#!/usr/bin/env bash
# Example external gate for xdlc-agent (v2 plugin protocol).
# stdin:  {"repo":"…"}
# stdout: {"ok":true|false,"evidence":{…}}
set -euo pipefail
repo="$(cat | sed -n 's/.*"repo"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
printf '{"ok":true,"evidence":{"repo":"%s","note":"example-external-gate always passes"}}\n' "$repo"
