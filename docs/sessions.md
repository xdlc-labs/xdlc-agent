# Fix sessions

Every Fix writes a **session**: the exact prompt the coding agent received, everything it printed, and the patch it produced. Audit history tells you a Fix ran and whether it worked. A session tells you *what the agent was thinking with*, which is what you actually need at 09:00 when reviewing something that ran at 03:00.

On by default. Nothing is uploaded anywhere — these are files on the daemon host.

## What gets written

One directory per Fix, under `sessions/` by default:

```
sessions/20260905T010514Z-example-service/
  meta.json      repo, signal, provider, status, outcome, attempts, duration, base/head SHA, cost
  prompt.txt     the full prompt: rules, past lessons, failure evidence, instruction
  plan.txt       diagnose-pass plan (only when agent.fix_plan is on)
  output.txt     everything the agent CLI printed
  prompt-2.txt   attempt 2's prompt (only when agent.fix_attempts sent the agent back in)
  output-2.txt   attempt 2's output
  diff.patch     committed changes since the Fix started, plus anything left uncommitted
```

The session id is also written into the audit record and `BACKLOG.md` as `session_id=…`, so a row in the console **Activity** feed points at the directory that explains it.

## Reading one

```sh
xdlc sessions ls                       # newest first
xdlc sessions ls --repo example-service --limit 5
xdlc sessions show 20260905T010514Z-example-service
xdlc sessions show 20260905T010514Z-example-service --diff
xdlc sessions show 20260905T010514Z-example-service --prompt
xdlc sessions show 20260905T010514Z-example-service --output
```

`--path` prints the directory, for piping into an editor:

```sh
$EDITOR "$(xdlc sessions show 20260905T010514Z-example-service --path)"
```

`ls` reads the same directory the daemon writes, so it works while the daemon is running and after it stops.

## Why you want it

- **Reviewing an automated Fix.** Read `diff.patch` before you trust the push, without cloning anything.
- **A Fix that did the wrong thing.** `prompt.txt` shows whether the agent was given bad evidence or good evidence and reasoned badly. Those have different remedies: better gate output versus better rules.
- **A Fix that did nothing.** `output.txt` usually says why in plain language, where the log line only kept the first 2000 characters. `meta.json`'s `outcome` and `summary` carry the agent's own one-line verdict ([Fix modes](fix-modes.md)).
- **A Fix that took more than one try.** `attempts` above 1 means the gate re-check was still red and the agent was sent back in. Compare `prompt.txt` against `prompt-2.txt` to see what it was told the second time, and `output.txt` against `output-2.txt` for what it did differently. `xdlc sessions show <id> --output --attempt 2` prints one directly.
- **Tuning rules.** The prompt shows exactly which of your `AGENTS.md` / `CLAUDE.md` rules survived truncation. See [Rules and skills](rules-and-skills.md).

## Configuration

```yaml
agent:
  sessions:
    enabled: true          # default; false records nothing
    dir: sessions          # default, relative to the daemon's working directory
    retain: 720h           # default 30 days
    max_file_bytes: 2097152  # default 2 MiB per artifact
```

Old sessions are pruned in the background, at most once an hour. Force it:

```sh
xdlc sessions prune
```

In Docker or Kubernetes, `sessions/` lives inside the container unless you mount it. Point `dir` at a mounted path if you want sessions to survive a restart:

```yaml
agent:
  sessions:
    dir: /var/lib/xdlc/sessions
```

## Security

**Session files are not scrubbed.** A Fix prompt embeds CI logs, and CI logs sometimes embed secrets that leaked into a build. The diff contains your source. Treat `sessions/` exactly like the build logs it came from:

- The directory is created `0700` and every file `0600`.
- Nothing is uploaded, and no API endpoint serves session content today.
- Anyone with read access to the daemon host can read them. That is the same trust boundary as `BACKLOG.md` and the audit database.
- Before pasting a prompt or diff into a ticket, read it first.

Set `enabled: false` if your host's disk is outside your compliance boundary. See [Security](SECURITY.md).
