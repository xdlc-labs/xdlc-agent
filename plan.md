# Plan: borrow from Spotify Xirp

Source: <https://backstage.spotify.com/docs/xirp> (+ `projects`, `sessions`, `workspaces`,
`xirp-and-portal`, `workspaces/launching-sessions`).

Xirp is a macOS desktop app that runs many coding-agent sessions (Claude Code, Codex, Gemini)
across projects, one Git worktree per task, with persistent sessions, a status minimap,
rules/skills tabs, and optional upload of the full session transcript to a Portal Workspace
so teammates and *future agents* can pull it back through MCP.

xdlc-agent is a headless daemon, not a desktop app, so the UI parts do not transfer. The
ideas below do.

Team-scale and org-scale borrows (session sharing, fleet views, org repo ingestion, RBAC over
transcripts) live in `xdlc-enterprise/strategy/XIRP-PLAN.md`, not here.

---

## Shipped

| Xirp idea | What landed | Docs |
|-----------|-------------|------|
| Session transcript kept and re-readable | `internal/session` writes prompt, agent output, diff, `meta.json` per Fix; `xdlc sessions ls/show/prune`; `session_id` in audit + `BACKLOG.md`; `agent.sessions.*` | [sessions.md](docs/sessions.md) |
| Rules tab aggregating `CLAUDE.md` / `AGENTS.md` | `CLAUDE.md`, `.xdlc/rules.md` and daemon-wide `agent.rules_file` joined the read set; per-file 8 KB cap; duplicates dropped; `xdlc doctor` prints what each repo contributes | [rules-and-skills.md](docs/rules-and-skills.md) |
| "Describe the goal" when starting a session | Optional `instructions` on Manual Fix (console dialog + `POST /api/actions/fix`), trusted-block placement, length-only in audit | [rules-and-skills.md](docs/rules-and-skills.md) |
| Gemini as a supported agent | `gemini` provider (`GEMINI_API_KEY`, `-p --yolo`), doctor + console + demo; opt-in in the image via `--build-arg GEMINI_CLI_VERSION` | [fix-modes.md](docs/fix-modes.md) |
| Import repos from a parent folder | `xdlc init --scan <dir>` seeds `repos:` from local checkouts with a GitHub origin | [configuration.md](docs/configuration.md) |
| Worktree per task | `agent.worktree` (on by default): each Fix runs in its own `git worktree` on an `xdlc/<session id>` branch under `repos/.worktrees/`; the per-repo Fix cap is gone, the agent commits and xdlc pushes, failed runs keep their worktree for `keep_failed` | [fix-modes.md](docs/fix-modes.md) |

---

## Still open

Ordered by value ÷ effort.

### 1. Feed past sessions into the next prompt (S, high)

**Xirp:** an uploaded transcript "gives future sessions access to prior context through MCP".

**Today:** `LESSONS.md` keeps one 200-character line per outcome, now carrying the agent's
own verdict summary rather than just the symptom. The sessions on disk are still richer than
that line and remain unused by the agent.

**Change:** `FixPrompt` gains a `priorSessions` block after `lessons`: for the last 2–3
sessions on this repo with the same `source`, the first ~40 lines of `diff.patch` plus
status. Cap 8 KB, trusted block. Optionally a `summary.md` written by a cheap second pass
(`agent.sessions.summarize`, default off).

**Measure before keeping:** Fix success rate on the demo repo, before and after.

### 2. Live Fix states (S, high)

**Xirp:** sessions show Working / Idle / Waiting / Finished-or-failed in a minimap.

**Today:** `FixQueueStats()` returns two integers; the console shows `fix_queue_depth`.

**Change:**

- Emit `queued | cloning | planning | fixing | pushing | verifying | ok | error` over the
  existing SSE hub, keyed by session id so the console collapses them into one row.
- `GET /api/fixes/active` → `[{session_id, repo, source, provider, state, since}]`.
- Overview gets an "in flight" strip; Actions shows the same during a manual Fix.
- **Stuck, not Waiting.** A headless agent cannot ask for input, so the analogue is a stall
  watchdog: no output for `agent.stall_timeout` while the process lives → kill the group,
  `escalate=stalled`. Note this only works with a streaming output format, so it must stay
  opt-in (`claude -p --output-format json` prints nothing until it exits). Wire it together
  with a switch to `stream-json`, not before.

### 3. Console view of a session (S, medium)

The recordings exist but are CLI-only. Add operator-token endpoints
(`GET /api/sessions`, `/{id}`, `/{id}/diff`, `/{id}/prompt`) and expand a `/repos/$id`
timeline row into a panel with meta, diff (reuse `doc-code.tsx`) and output tail.

Serving unscrubbed prompts over HTTP is a real exposure step, unlike writing them to a
0600 file: gate it on the operator role and say so in `docs/SECURITY.md`.

### 4. Context on demand: `xdlc mcp` (L, high)

**Xirp:** "The agent loads detail only when needed. This avoids placing every Workspace
document in the initial prompt."

**Today:** the dispatcher inlines failed-job logs and metrics, trimmed to 32 KB. For a large
CI matrix the useful line is often the one cut. The agent cannot ask for more.

**Change:** `xdlc mcp --session <id>` as a stdio MCP server with read-only tools scoped to
that session's repo: `ci_logs(job?, grep?, tail?)`, `ci_run(run_url)`, `prod_metrics(query?)`,
`prior_sessions(limit)`, `session_diff(id)`, `lessons()`, `backlog(tail)`, `repo_config()`.
Passed to the CLI via its own MCP config flag. The prompt then keeps only conclusion,
`run_url` and the last 60 log lines. Behind `agent.mcp.enabled`, default off until measured.

Every tool call is appended to the session recording, so "what did it look at" stays
answerable.

### 5. Grid view of live Fixes (M, low)

**Xirp:** `Cmd+G` grid of live terminals. A read-only `/fixes` route, one card per in-flight
Fix, output tail over SSE. Needs item 2 first.

---

## Not borrowing

macOS app, tmux-backed interactive terminals, Portal OAuth, keyboard minimap. All of them
assume a human at a keyboard; xdlc runs unattended.

## Sequencing

1. Item 1 first; it is small and reuses what already lands on disk. Worktrees have shipped,
   so a prior session's diff now describes an isolated run rather than a shared clone.
2. Item 2 only alongside the switch to a streaming provider output format.
3. Item 4 after item 1, since `prior_sessions` is one of its tools.
4. Item 5 needs item 2 first.
5. Docs to touch each time: `architecture.md`, `fix-modes.md`, `sessions.md`,
   `configuration.md`, `api-reference.md`, `CHANGELOG.md`.
