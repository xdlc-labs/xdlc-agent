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

Related knobs (see `config.example.yaml`): `fix_reverify`, `ci_rerun_before_fix`, `fix_plan`, `max_concurrent_fixes`, `fix_budget`.

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
