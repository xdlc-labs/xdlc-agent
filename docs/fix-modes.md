# Fix modes

When CI or DEV smoke fails, the daemon runs a coding-agent **Fix**.

## Provider

```yaml
agent:
  mode: subprocess
  provider: claude    # claude | codex | cursor
  timeout: 10m
```

| Provider | CLI on PATH | API key env |
|----------|-------------|-------------|
| claude | `claude` | `ANTHROPIC_API_KEY` |
| codex | `codex` | `OPENAI_API_KEY` |
| cursor | `cursor-agent` | `CURSOR_API_KEY` |

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

## Permissions

- **direct:** push to the integration branch
- **pr:** create branches + PRs; read checks for the queue

## Verification status

Real `fix_mode: pr` against GitHub: [#27](https://github.com/xdlc-labs/xdlc-agent/issues/27). Direct Fix is covered by `xdlc demo` and Manual Fix in the console.
