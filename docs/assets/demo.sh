#!/usr/bin/env bash
# Regenerates the README demo. Run from a clone of this repository:
#
#   asciinema rec --cols 84 --rows 34 -q -i 1.5 -c docs/assets/demo.sh docs/assets/demo.cast
#   agg --font-size 15 --theme asciinema --fps-cap 20 docs/assets/demo.cast docs/assets/demo.gif
#
# XDLC_DEMO_PROVIDER picks the coding agent. The default is the real
# thing: `claude`, which needs the CLI on PATH and its key, and is what
# the README GIF shows -- a stub agent proves the loop but not the
# product. XDLC_DEMO_PROVIDER=fake records the keyless version.
#
# `xdlc demo` builds its own throwaway repo and origin in a temp dir, so
# this touches nothing outside $work.
set -u

repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

go build -o "$work/xdlc" "$repo/cmd/xdlc-agent" || exit 1
export PATH="$work:$PATH"

provider="${XDLC_DEMO_PROVIDER:-claude}"

prompt=$'\033[1;36m$\033[0m '
say() { printf "\033[2m%s\033[0m\n" "$1"; sleep 1.1; }
type_out() {
  printf '%s' "$prompt"
  local i
  for ((i = 0; i < ${#1}; i++)); do
    printf '%s' "${1:$i:1}"
    sleep 0.018
  done
  printf '\n'
  sleep 0.35
}
run() {
  type_out "$1"
  eval "$1" 2>&1
  sleep 1.0
}

say "# no config, no daemon, no GitHub. one throwaway repo whose CI is red."
run "xdlc demo --provider $provider --pace 500ms"
printf "\033[1;32mexit 0\033[0m  \033[2m- red test fixed by the agent, promoted, then reverted on a prod breach\033[0m\n"
sleep 2.6
