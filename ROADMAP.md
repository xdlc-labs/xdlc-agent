# Roadmap

Where `xdlc-agent` is going next, and why. **This is a roadmap, not a commitment** — items
are sized and ordered by what looks most useful per unit of work, and any of them can be
re-ordered, reshaped, or dropped once measured. Nothing here has a date. What has actually
shipped is in [CHANGELOG.md](CHANGELOG.md).

Sizes are rough effort (S / M / L); value is our own guess at operator payoff.

## Already landed

The pieces the open items below build on:

- **Fix sessions** — every Fix records its prompt, agent output, diff and `meta.json` under
  `sessions/`; `xdlc sessions ls|show|prune`, `session_id` in the audit store and
  `BACKLOG.md`, config under `agent.sessions.*`
  ([Fix sessions](https://xdlc.dev/agent/docs/sessions))
- **Worktree per Fix** — `agent.worktree`, on by default: each Fix runs in its own
  `git worktree` on an `xdlc/<session id>` branch under `repos/.worktrees/`, so two Fixes on
  one repo run concurrently, the agent commits and xdlc pushes, and failed runs keep their
  worktree for `keep_failed` ([Fix modes](https://xdlc.dev/agent/docs/fix-modes))
- **Repo conventions as rules** — `AGENTS.md`, `CLAUDE.md`, `.xdlc/rules.md`,
  `.xdlc/skills/*.md` and a daemon-wide `agent.rules_file` all feed the Fix prompt, with an
  8 KB per-file cap, duplicates dropped, and `xdlc doctor` printing what each repo
  contributes ([Rules and skills](https://xdlc.dev/agent/docs/rules-and-skills))
- **Operator instructions on Manual Fix** — optional free text in the console dialog and on
  `POST /api/actions/fix`, placed in a trusted block, length-only in the audit trail
- **Four agent providers** — `claude`, `codex`, `cursor` and `gemini`, each with a headless
  default invocation; Gemini is opt-in in the container image via
  `--build-arg GEMINI_CLI_VERSION` ([Fix modes](https://xdlc.dev/agent/docs/fix-modes))
- **Seeding a config from local checkouts** — `xdlc init --scan <dir>` fills `repos:` from
  Git checkouts that have a GitHub origin

## Open items

Ordered by value ÷ effort.

### 1. Feed past sessions into the next Fix prompt (S, high)

**Today:** `LESSONS.md` keeps one 200-character line per outcome, now carrying the agent's own
verdict summary rather than just the symptom. The sessions on disk are far richer than that
line and the agent never sees them, so a second Fix on the same repo re-derives what the first
one already worked out.

**Change:** `FixPrompt` gains a `priorSessions` block after `lessons`: for the last 2–3
sessions on this repo with the same `source`, the first ~40 lines of `diff.patch` plus the
run's status. Cap the block at 8 KB, place it in a trusted block. Optionally a `summary.md`
written by a cheap second pass (`agent.sessions.summarize`, default off).

**Measure before keeping:** Fix success rate on the demo repo, before and after.

### 2. Live Fix states and a stall watchdog (S, high)

**Today:** `FixQueueStats()` returns two integers and the console shows `fix_queue_depth`. A
Fix that is running is indistinguishable from one that is wedged.

**Change:**

- Emit `queued | cloning | planning | fixing | pushing | verifying | ok | error` over the
  existing SSE hub, keyed by session id so the console collapses them into one row.
- `GET /api/fixes/active` → `[{session_id, repo, source, provider, state, since}]`.
- Overview gets an "in flight" strip; Actions shows the same during a Manual Fix.
- **Stalled, not waiting.** A headless agent cannot ask for input, so the failure mode to
  catch is a stall: no output for `agent.stall_timeout` while the process is still alive →
  kill the process group, record `escalate=stalled`. This only works with a streaming output
  format (`claude -p --output-format json` prints nothing until it exits), so it stays opt-in
  and ships together with a switch to `stream-json`, not before.

### 3. Console view of a session (S, medium)

The recordings exist but are reachable only from the CLI. Add operator-token endpoints
(`GET /api/sessions`, `/{id}`, `/{id}/diff`, `/{id}/prompt`) and let a `/repos/$id` timeline
row expand into a panel with meta, the diff (reuse `doc-code.tsx`) and the tail of the agent
output.

Serving unscrubbed prompts over HTTP is a real exposure step, unlike writing them to a 0600
file: gate it on the operator role and document that in [SECURITY.md](SECURITY.md).

### 4. Context on demand: `xdlc mcp` (L, high)

**Today:** the dispatcher inlines failed-job logs and metrics, trimmed to 32 KB. On a large CI
matrix the one useful line is often the one that got cut, and the agent has no way to ask for
more.

**Change:** `xdlc mcp --session <id>` as a stdio MCP server exposing read-only tools scoped to
that session's repo: `ci_logs(job?, grep?, tail?)`, `ci_run(run_url)`,
`prod_metrics(query?)`, `prior_sessions(limit)`, `session_diff(id)`, `lessons()`,
`backlog(tail)`, `repo_config()`. Passed to the agent CLI through its own MCP config flag. The
prompt then carries only the conclusion, the `run_url` and the last 60 log lines, and the
agent pulls detail when it needs it. Behind `agent.mcp.enabled`, default off until measured.

Every tool call is appended to the session recording, so "what did it look at" stays
answerable after the fact.

### 5. Grid view of live Fixes (M, low)

A read-only `/fixes` route: one card per in-flight Fix with its output tail streamed over SSE,
for watching a fleet-wide burst of Fixes at once. Needs item 2 first.

## Sequencing

1. Item 1 first — it is small and reuses what already lands on disk. Worktrees have shipped,
   so a prior session's diff now describes an isolated run rather than a shared clone.
2. Item 2 only alongside the switch to a streaming provider output format.
3. Item 4 after item 1, since `prior_sessions` is one of its tools.
4. Item 5 needs item 2 first.
5. Each of these touches the hosted guides — [architecture](https://xdlc.dev/agent/docs/architecture),
   [Fix modes](https://xdlc.dev/agent/docs/fix-modes),
   [Fix sessions](https://xdlc.dev/agent/docs/sessions), configuration and the API reference —
   plus [CHANGELOG.md](CHANGELOG.md).

## Out of scope

`xdlc-agent` is a headless daemon that runs unattended. Interactive, human-at-a-keyboard
features — an attached terminal per session, a desktop app, keyboard-driven session switching
— are not on this roadmap. Team- and org-scale concerns (sharing sessions between people,
fleet-wide views across many operators, RBAC over transcripts) are tracked separately and
outside this repository.

Want something here sooner, or something that is not here at all? Open an issue — see
[CONTRIBUTING.md](CONTRIBUTING.md).
