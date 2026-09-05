# xdlc-agent vs alternatives

Honest map of what peers own vs what this daemon owns. One screen for “how is this different from Copilot Autofix / Flagger?”

| Tool | What it is | Where it stops | vs xdlc-agent |
|------|------------|----------------|---------------|
| **GitHub Copilot Autofix** | PR suggestions for CodeQL / Dependabot findings | No continuous prod SLO loop; no promote/revert policy; lives inside GitHub | We react to **CI / DEV smoke / prod health** with Fix **and** Promote/Revert under your gates |
| **PipelineHealer / similar CI healers** | Rerun / patch failed CI | Usually CI-scoped; weak multi-env promote story | Same CI Fix idea; we add **rerun ladder** (roadmap), DEV smoke, prod revert, fleet policy |
| **STITCH / self-heal CI research** | Re-verify after patch | Often demo/research shape | We want the same honesty: Fix ok only when gate green again (roadmap #2) |
| **TALOS-style patient-zero** | Fix upstream of a failure cascade | Research / specialized | Fleet `depends_on` + escalate today; patient-zero Fix on root is roadmap (#4) |
| **Flagger / Argo Rollouts / Kargo** | Progressive delivery, canaries, promotions | Traffic/shift + analysis — not “paste failing logs into a coding agent” | Complementary: they own **how** traffic moves; we own **policy-gated agent actions** when gates fail |
| **Praxis / agent platforms** | General agent orchestration | Not a delivery-control plane tied to your GitOps branches | We are a **thin daemon on your existing CI+GitOps**, not a new agent OS |
| **“Paste logs into ChatGPT”** | Human loop | No audit, no policy, no promote/revert | Daemon receives the same signals, runs **your** CLI (`claude`/`codex`/`cursor`), writes audit + backlog |

## Short answers

**Copilot Autofix?** Security/dependency PR nits inside GitHub. We are overnight delivery automation (red CI → Fix, green smoke → Promote, SLO breach → Revert).

**Flagger / Rollouts?** Keep them for canary/analysis. Point Alertmanager / probes at us when you want an agent (or git revert) inside the same policy you already trust.

**Another agent framework?** No. One loop, BYO coding-agent CLI. CI Fix by default; GitOps / prod gates optional. MIT, self-hosted, no SaaS in the path.

See also: [why not a GitHub Action](why-not-github-action.md), [architecture](architecture.md).
