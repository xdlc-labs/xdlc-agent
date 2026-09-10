# Roadmap

Where `xdlc-agent` is going next, and why. **This is a roadmap, not a commitment** — items
are sized and ordered by what looks most useful per unit of work, and any of them can be
re-ordered, reshaped, or dropped once measured. Nothing here has a date. What has actually
shipped is in [CHANGELOG.md](CHANGELOG.md).

## Landed in 1.0

Everything the 0.x roadmap had open shipped in 1.0.0, and the pieces it was built on:

- **Fix sessions** — every Fix records its prompt, agent output, diff and `meta.json` under
  `sessions/`; `xdlc sessions ls|show|prune`, `session_id` in the audit store and
  `BACKLOG.md`, config under `agent.sessions.*`
  ([Fix sessions](https://xdlc.dev/agent/docs/sessions))
- **Console view of a session** — operator-only `GET /api/sessions`, `/{id}`, `/{id}/diff`,
  `/{id}/prompt`, `/{id}/output`; a `/repos/$id` timeline row expands into the recording
- **Context on demand: `xdlc mcp`** — a stdio MCP server per Fix with `ci_logs`, `ci_run`,
  `prod_metrics`, `prior_sessions`, `session_diff`, `lessons`, `backlog`, `repo_config`,
  wired into all four agent CLIs behind `agent.mcp.enabled`, every call recorded in the
  session's `tools.jsonl`
- **Live Fix grid** — `/fixes` shows each running agent's output as it prints, streamed as
  the `fix_output` SSE event beside the `fix_state` phases
- **Worktree per Fix** — `agent.worktree`, on by default: each Fix runs in its own
  `git worktree` on an `xdlc/<session id>` branch, so two Fixes on one repo run concurrently
  ([Fix modes](https://xdlc.dev/agent/docs/fix-modes))
- **Repo conventions as rules** — `AGENTS.md`, `CLAUDE.md`, `.xdlc/rules.md`,
  `.xdlc/skills/*.md` and a daemon-wide `agent.rules_file` all feed the Fix prompt
  ([Rules and skills](https://xdlc.dev/agent/docs/rules-and-skills))
- **Four agent providers** — `claude`, `codex`, `cursor` and `gemini`, each with a headless
  default invocation ([Fix modes](https://xdlc.dev/agent/docs/fix-modes))
- **One-shot Fix without the daemon** — `xdlc fix <run-url>` and the `action.yml` wrapper
- **Seeding a config from local checkouts** — `xdlc init --scan <dir>`

## Open items

Nothing is scheduled. Candidates, unsized until someone asks for them:

- **Measure `agent.mcp`.** It shipped default-off. The question is whether a Fix that can
  pull the full logs fixes more, or fixes the same for more tokens; `tools.jsonl` next to
  `meta.json` cost fields is the data. Flip the default only on that evidence.
- **`ci_logs` for the other gates.** Today the tool serves CI runs. A Revert triggered by a
  prod-health breach has nothing comparable to hand the agent; a `prod_metrics` range query
  around the breach is the obvious next tool.
- **Multi-arch image.** The release image is linux/amd64. arm64 waits on a native runner.

## Out of scope

`xdlc-agent` is a headless daemon that runs unattended. Interactive, human-at-a-keyboard
features — an attached terminal per session, a desktop app, keyboard-driven session switching
— are not on this roadmap. Team- and org-scale concerns (sharing sessions between people,
fleet-wide views across many operators, RBAC over transcripts) are tracked separately and
outside this repository.

Want something here sooner, or something that is not here at all? Open an issue — see
[CONTRIBUTING.md](CONTRIBUTING.md).
