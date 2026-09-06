# Fix modes

When CI or DEV smoke fails, the daemon runs a coding-agent **Fix**.

## Provider

```yaml
agent:
  mode: subprocess
  provider: claude    # claude | codex | cursor | gemini
  timeout: 10m
```

| Provider | CLI on PATH | API key env |
|----------|-------------|-------------|
| claude | `claude` | `ANTHROPIC_API_KEY` |
| codex | `codex` | `OPENAI_API_KEY` |
| cursor | `cursor-agent` | `CURSOR_API_KEY` |
| gemini | `gemini` | `GEMINI_API_KEY` |

Cursor and Gemini run with their approval prompts disabled (`--trust` and `--yolo`), because a headless Fix has no TTY to answer them. Override the whole argv with `agent.args` if that is not what you want.

`xdlc doctor` checks the binary for the configured provider.

Console **Settings** can store a browser-local provider + key for **Manual Fix** only (`X-XDLC-Agent-*` headers). That key is not written to audit/disk on the server beyond the subprocess env for that run.

## `fix_mode`: direct vs pr

```yaml
agent:
  # fix_mode: direct   # default — commit + push to develop
  # fix_mode: pr       # open a Fix PR instead
```

- **direct** — subagent commits on the worktree and pushes the integration branch.
- **pr** — opens a scratch-branch PR; console **Actions** shows the Fix-PR work queue (title, age, CI, merged).

Related knobs (see `config.example.yaml`): `fix_reverify`, `fix_attempts`, `ci_rerun_before_fix`, `fix_plan`, `max_concurrent_fixes`, `fix_budget`.

## Where a Fix runs: one worktree per Fix

```yaml
agent:
  worktree:
    enabled: true      # default
    keep_failed: 24h
```

Each Fix gets its own git worktree, checked out from `origin/<branch>` on
a scratch branch named after the Fix session (`xdlc/<session id>`). The
shared clone under `repos/<name>` is never edited by an agent; worktrees
live beside it under `repos/.worktrees/<repo>/<session id>`.

This buys two things:

- **Two Fixes for one repo can run at once.** They cannot see each
  other's edits, so the per-repo serialization is gone and
  `max_concurrent_fixes` is the only cap left.
- **A Fix that dies mid-edit costs nothing.** It used to leave the shared
  clone dirty, which the next operation silently hard-reset. Now the
  wreckage is confined to a directory you can read.

The trade is that the agent no longer pushes. Two worktrees cannot both
have the tracked branch checked out, so there is nothing for the agent to
push to: it commits on its scratch branch, and xdlc pushes those commits
to the tracked branch (or, in `pr` mode, to the PR branch) once the run
returns. The prompt tells the agent exactly this.

Git credentials are still passed to the agent's subprocess, since it may
legitimately need to read from the remote, but writing to a branch anyone
else can see is no longer something a Fix run does on its own.

That push is a plain, non-force push. If the branch moved while the agent
was working, it is refused and the Fix fails loudly rather than
overwriting whatever landed first.

A **successful** Fix has its worktree removed immediately. A **failed**
one keeps it for `keep_failed` (default 24h) so you can see what the
agent actually left behind; the sweep runs at daemon start and before
each new Fix on that repo.

```sh
xdlc sessions show <id>          # branch: xdlc/<id>
ls repos/.worktrees/<repo>/      # worktrees kept from failed Fixes
```

Set `enabled: false` to go back to one shared clone per repo, with the
agent pushing and one Fix per repo at a time.

## The agent's verdict

A coding-agent CLI exits 0 whether it pushed a fix or decided the failure
was out of its reach and left a `BACKLOG.md` note. The exit code cannot
tell those apart, so every Fix prompt ends by asking the agent to print
one line of JSON as its last output:

```json
{"xdlc_outcome": "fixed", "summary": "bumped the pinned go-github version"}
```

| Outcome | Meaning | What xdlc does |
|---------|---------|----------------|
| `fixed` | Committed and pushed a change it believes resolves the failure | Continue: re-check the gate if `fix_reverify` is on |
| `gave_up` | Not fixable from this repo alone; the `BACKLOG.md` note is written | Fail the Fix with `escalate=agent_gave_up`; do not retry |
| `needs_human` | A fix exists but needs a human decision | Fail the Fix with `escalate=agent_needs_human`; do not retry |

The summary lands in three places an operator already reads: the Activity
row's evidence (`agent_outcome`, `agent_summary`), the session's
`meta.json`, and the `LESSONS.md` line that the *next* Fix on this repo is
shown.

An agent that prints no verdict line is treated exactly as before this
existed: `OUTCOME` shows `-`, nothing escalates, and the gate re-check is
what decides. Nothing in the loop depends on a well-behaved agent.

## `fix_attempts`: retrying a Fix that did not land

```yaml
agent:
  fix_reverify: true   # required
  fix_attempts: 2      # default 1 (single shot)
```

With `fix_reverify` on, a Fix is only ok once the failing gate goes green
again. `fix_attempts` decides what happens when it does not: rather than
recording one failed Fix, the agent is run again, and this time it is told

- that this is attempt N of M and that its previous push did **not** fix the gate,
- what the previous attempt reported doing, in its own words, from its verdict,
- why the re-check failed,
- and the logs of the run that is failing **now** — re-fetched from the
  re-check's `run_url`, not the stale run that opened the Fix.

Its earlier commits are already in the working tree, so it can read them
with `git log` before trying something different.

The ladder stops early when the agent reports `gave_up` or `needs_human`:
it has already answered, and asking again spends tokens to hear the same
thing. Otherwise it stops at the ceiling with `escalate=reverify_failed`.

Each attempt is recorded separately — `prompt.txt` / `output.txt` for the
first, `prompt-2.txt` / `output-2.txt` for the second — so you can read
what changed between tries:

```sh
xdlc sessions show <id>                 # OUTCOME, SUMMARY, ATTEMPTS
xdlc sessions show <id> --prompt --attempt 2
```

Cost is the obvious trade: `fix_attempts: 3` is up to three agent runs for
one failure. `total_cost_usd` in the audit row is the sum across attempts,
and `xdlc_agent_fix_retries_total` counts the extra runs, so you can see
what the ladder is buying before widening it. `fix_budget` still caps the
whole thing in wall-clock time.

Without `fix_reverify` there is no signal that attempt 1 failed, so
`fix_attempts` is clamped back to 1 and the daemon logs a warning at
startup of the Fix.

## What the agent is told

The prompt is assembled from your repo's instruction files, past lessons, and the failing gate's evidence. See **[Rules and skills](rules-and-skills.md)** for the full order, the size caps, and the optional per-run operator note on Manual Fix.

## What the agent did

Every Fix records its prompt, output and diff under `sessions/`. See **[Fix sessions](sessions.md)**.

```sh
xdlc sessions ls --repo example-service
xdlc sessions show <id> --diff
```

## Permissions

- **direct:** push to the integration branch
- **pr:** create branches + PRs; read checks for the queue

## Verification status

Real `fix_mode: pr` against GitHub: [#27](https://github.com/xdlc-labs/xdlc-agent/issues/27). Direct Fix is covered by `xdlc demo` and Manual Fix in the console.
