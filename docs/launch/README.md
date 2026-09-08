# Launch kit

Drafts for the first public push. Nothing here is posted automatically. Edit, then post by hand, in this order, on one day.

## Before posting (blockers)

1. **Make the GHCR package public.** `docker pull ghcr.io/xdlc-labs/xdlc-agent:<tag>` must work anonymously. Org → Packages → xdlc-agent → Package settings → Change visibility. A broken pull on launch day is the top comment.
2. **Dogfood, in public.** One down: [#51](https://github.com/xdlc-labs/xdlc-agent/pull/51), a real flaky-test fix on this repo for $1.63, screenshotted under `xdlc fix` in the README (`docs/assets/pr.png`, regenerate with `docs/assets/pr.sh`). Get to **five** the same way, across this repo and [airlock](https://github.com/xdlc-labs/airlock), and add each to the "Fixes in the wild" table. Merge them: an open PR from the author reads weaker than a merged one. Receipts beat claims.
3. **Cut a release** so the install one-liner and the Action resolve to a tag that has `xdlc fix`. Bump `appVersion` in the Helm chart and the README pins together (`make check-versions`).
4. **Check the hero renders in both themes.** `docs/assets/loop-light.svg` and `-dark.svg` come from `python3 docs/assets/loop.py`; open the README on GitHub with the theme toggled both ways before posting, since the whole pitch is above the fold. Then watch the GIF once more at `docs/assets/demo.gif`. It is a real `claude` run, not the stub agent, which is the half a skeptic will poke at first. Re-record with the two commands in the header of `docs/assets/demo.sh` if the output changed, and keep the theme identical to [airlock](https://github.com/xdlc-labs/airlock)'s.
5. **Be online for six hours after posting.** Answer every comment in the first hour.

## Show HN

**Title** (80 chars max, no marketing words):

> Show HN: Xdlc – self-hosted CI fix: when CI fails, your own coding agent opens a PR

**Text:**

> The one design decision worth arguing about: the coding agent is not the top of the loop, policy is. Three signals come in (CI conclusion, a DEV smoke probe, a prod SLO breach) and policy returns fix, promote, revert or noop before an agent is reachable at all. Only fix runs an agent. The README's first image is that state machine rather than a screenshot, because "agent opens a PR" is the commoditized part.
>
> There is a real receipt in there too: a pull request this opened against its own repository, for a flaky concurrency test in its own suite, for $1.63.
>
> I run a small Go daemon next to my repos. When a GitHub Actions run fails, it hands the failing job's logs and the repo's AGENTS.md/CLAUDE.md to whichever coding-agent CLI is on PATH (claude, codex, cursor, gemini), lets the agent commit in its own git worktree, pushes, and opens a PR. The prompt, the agent's output, the diff and the cost are written to disk per run.
>
> Try it without keys: `curl -fsSL https://raw.githubusercontent.com/xdlc-labs/xdlc-agent/main/scripts/install.sh | bash && xdlc demo`. With keys, `xdlc fix <run-url>` does one real run, no daemon. There is also a GitHub Action wrapper for one repo.
>
> Things I got wrong along the way that shaped the design: a Fix that committed nothing used to be recorded as a success (#34); a PR could be requested for a branch that was never pushed (#37); a Prometheus query that matched no series read as "healthy" and disabled breach detection (#48). Each of those is now a failing state you can see.
>
> Nothing phones home. MIT. I would like to hear where it breaks on your repos.

**First comment (post immediately, as author):**

> A few honest limits: the audit DB is single-writer, so it is one replica. Promote and revert are opt-in and assume develop → main fast-forward plus ArgoCD/Prometheus; most people should ignore them and use CI Fix only. Cost per Fix on my repos has been $0.30–$1.20 with claude; the demo uses a fake agent so it costs nothing.

## X / Twitter thread

1. CI broke at 3am. I woke up to a green PR. The agent was mine, the keys never left my box, and I could read exactly what it was told. Open-sourced the daemon that does this: xdlc-agent. 🧵 [loop diagram]
2. One command, no daemon, no config: `xdlc fix https://github.com/you/repo/actions/runs/123`. Clones, reads the failing job logs + your AGENTS.md, runs claude/codex/cursor/gemini in a worktree, pushes, opens the PR. [screenshot of the CLI output ending in PR: …]
3. Every Fix gets its own git worktree on an `xdlc/<session>` branch. Two fixes on one repo run side by side. A run killed mid-edit can't dirty your clone. The agent commits; xdlc pushes.
4. Receipts. Prompt, output, diff, verdict, cost, per run: `xdlc sessions show <id> --diff`. A Fix that committed nothing is recorded as "delivered nothing", not as a success. That one bit me (#34).
5. Optional legs: fast-forward develop → main after DEV smoke passes; revert main when prod p95 breaches. Off by default. Most people want CI Fix only.
6. Self-hosted, MIT, nothing phones home. Try the 30-second demo with a fake agent, then point it at a real red run. Repo: github.com/xdlc-labs/xdlc-agent — tell me where it breaks.

## Reddit

**r/selfhosted** — title: "Self-hosted daemon that fixes your failed CI with your own coding agent (claude/codex/cursor/gemini) and opens the PR. MIT, nothing phones home."

**r/devops** — title: "Open-source: when GitHub Actions fails, run your coding agent on the logs and open a PR. Worktree per fix, cost and prompt recorded."

Body for both (adapt): the Show HN text, minus the "Show HN" framing, plus the GIF and the `xdlc fix` snippet. Lead with the 30-second demo. Do not cross-post the same hour; HN first, Reddit next morning.

## Blog post (dev.to / own site), same week

**Title:** "Every CI fix gets its own worktree, and other things I got wrong"

Outline:
1. The 3am story, two paragraphs, then the GIF.
2. Why the agent never pushes: worktree per Fix, xdlc owns the push, `HasCommits` decides "delivered".
3. Three bugs that became design rules: #34 (nothing delivered ≠ success), #37 (no PR for an unpushed branch), #48 (no data ≠ healthy). Show the before/after audit rows.
4. What a Fix costs: real `total_cost_usd` numbers from the sessions dir, per provider.
5. Why it is a daemon and not (only) an Action, in five sentences, with a link to the Action wrapper for people who want the on-ramp anyway.
6. Try it: demo, `xdlc fix`, Action, daemon. Ask for failure reports.

## After launch

- Pin a "Fixes in the wild" issue and ask people to post their PR links + cost.
- Turn every "it broke on my repo" into a session file request; fix in public with a linked PR.
- Ship the top asked-for item from ROADMAP.md within the week and post the follow-up.
