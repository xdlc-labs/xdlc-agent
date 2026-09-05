# Governance

Honest picture of how **xdlc** is run today. This is a small open-source
project, not a foundation with a board.

## Maintainer model

**Solo / small.** Primary maintainer reviews PRs, cuts releases, and owns
security response. There is no formal committer ladder, TSC, or paid
support org yet. If that changes, this doc updates first.

Day-to-day: issues and PRs on GitHub; no separate RFC tooling.

## How decisions get made

| Scope | Process |
|---|---|
| Small fix, docs, tests | Open a PR. Maintainer review + merge. |
| Behavior / API / config change | PR + clear changelog note. Breaking changes follow [docs/upgrade.md](docs/upgrade.md#compatibility-semver). |
| Large direction (deferred work, scope cut) | Open a tracking issue describing the direction and link it from the PR; note the tradeoff in [CHANGELOG.md](CHANGELOG.md). |
| Security-sensitive change | Prefer private coordinated disclosure first — see [SECURITY.md](SECURITY.md). |

Disagreement: discuss on the PR. Maintainer has final call while the
project stays solo/small. Consensus is preferred; silence after a
reasonable review window is not a veto.

## Contribution path

How to build, test, add a gate or provider, and open a PR:
**[CONTRIBUTING.md](CONTRIBUTING.md)**.

Community expectations: **[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)**
(Contributor Covenant). Reports of CoC violations go to the maintainers
via the contact listed in that file (or a private GitHub security advisory
if the report is also a security matter).

## Support

**Community-only.** Help is GitHub Issues (and discussions if enabled).
There is **no paid support tier**, no dedicated on-call from maintainers,
and **no contractual SLA**. Response time is best-effort. Production
incidents in *your* cluster are yours — see [docs/runbook.md](docs/runbook.md).

There is **no vendor uptime guarantee**. Availability is whatever your
cluster, storage, and dependencies deliver. Do not treat README defaults,
Helm probes, or gate intervals as a published SLO. Operator knobs:
[docs/capacity.md](docs/capacity.md).

| Kind | Where |
|---|---|
| Bugs, feature requests, docs gaps | Public GitHub Issues |
| Security vulnerabilities | Private advisory — [SECURITY.md](SECURITY.md) |

## Security and compliance

Vulnerability reporting, credential blast radius, and trust boundaries:
**[SECURITY.md](SECURITY.md)**. Deeper model:
[docs/threat-model.md](docs/threat-model.md).

Do not open public issues with exploit details.

**Compliance is not assessed.** No SOC 2, ISO 27001, or GDPR (or other
privacy-law) report from maintainers. xdlc is a community daemon you
run yourself — maintainers do not operate a hosted multi-tenant SaaS and
do not claim fitness for any regulated workload. **You** own compliance
for where you deploy. Detail and auditor pointers: [SECURITY.md](SECURITY.md#compliance).

## What this is not

- No SLA or paid support contract (see [Support](#support)).
- No SOC 2 / GDPR assessment (see [SECURITY.md](SECURITY.md#compliance)).
- No guarantee that every good idea lands; known gaps (e.g. HA — see
  [docs/disaster-recovery.md](docs/disaster-recovery.md#high-availability-not-implemented))
  are documented rather than silently dropped.
