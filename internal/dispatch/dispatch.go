// Package dispatch implements orchestrator.Dispatcher — the
// side-effecting half of an Action: run a subagent, revert, or promote.
package dispatch

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/metric"

	"github.com/xdlc-labs/xdlc-agent/internal/orchestrator"
	"github.com/xdlc-labs/xdlc-agent/internal/otel"
	"github.com/xdlc-labs/xdlc-agent/internal/promote"
	"github.com/xdlc-labs/xdlc-agent/internal/repos"
	"github.com/xdlc-labs/xdlc-agent/internal/subagent"
)

// PRRef is what FindPR reports back after a fix_mode: pr dispatch —
// deliberately not tied to any specific Git host client type, same
// decoupling as FetchLogs.
type PRRef struct {
	Number int
	URL    string
	State  string
}

// Dispatcher implements orchestrator.Dispatcher against real git repos
// (via Repos) and a real Claude subagent (via Subagent).
type Dispatcher struct {
	Repos     *repos.Manager
	Subagent  subagent.Runner
	FetchLogs func(ctx context.Context, runURL string) (string, error) // optional CI log ingest
	// FindPR looks up the PR opened for ownerRepo/branch after a
	// fix_mode: pr dispatch. Optional — nil skips PR lookup entirely
	// (fix_mode: direct never needs it). nil, nil (no error) means no
	// matching PR was found, not a failure.
	FindPR func(ctx context.Context, ownerRepo, branch string) (*PRRef, error)
	// CreatePR opens a PR when FindPR finds none after a pr-mode Fix.
	// Optional — if nil and no PR exists, Fix only logs a warning.
	CreatePR func(ctx context.Context, ownerRepo, head, base, title, body string) (*PRRef, error)
	Log      *slog.Logger
	Metrics  *otel.Metrics // optional
	// FixMode is "direct" (or empty) vs "pr" — passed to subagent.FixPrompt.
	FixMode string
	// FixPlan enables optional plan-then-patch two-pass Fix (issue #23).
	// Default false — one-shot FixPrompt.
	FixPlan bool
	// Lessons optional past-Fix inject (issue #19). nil skips.
	Lessons interface {
		Record(repo, source, outcome, symptom string) error
		ForRepo(repo string, k int) string
	}
	// Reverify, when set, is called after a successful Fix subagent run
	// (issue #2). Non-nil error means the gate is still red — Fix fails.
	Reverify func(ctx context.Context, s orchestrator.Signal) error
	// Provider routing (v2): Route "cheapest" picks among Providers.
	Route           string
	Providers       []string
	DefaultProvider string
	RouteMinSuccess float64
	// ProviderStats optional; when nil and Route is cheapest, uses DefaultProvider.
	ProviderStats func() map[string]ProviderStats
	// NewRunner builds a Runner for a provider name; nil → always use Subagent.
	NewRunner func(provider string) subagent.Runner
	// fixSem caps concurrent Fix runs. Nil = unlimited (tests); set via SetFixConcurrency.
	fixSem chan struct{}
	// fixRepoSem: cap 1 Fix per repo when fixSem is set (#9 fair drain).
	// ponytail: map+chan; upgrade weighted fair queue if >~50 repos contend.
	fixRepoMu  sync.Mutex
	fixRepoSem map[string]chan struct{}
	// FixBudget soft-cancels Fix after duration (#9). 0 = unlimited.
	FixBudget time.Duration
	fixWaiting  atomic.Int64
	fixInflight atomic.Int64
}

// New returns a Dispatcher.
func New(r *repos.Manager, s subagent.Runner, log *slog.Logger) *Dispatcher {
	return &Dispatcher{Repos: r, Subagent: s, Log: log}
}

// SetFixConcurrency caps concurrent Fix runs. n <= 0 clears the cap (unlimited).
func (d *Dispatcher) SetFixConcurrency(n int) {
	if n <= 0 {
		d.fixSem = nil
		return
	}
	d.fixSem = make(chan struct{}, n)
}

// FixQueueStats returns goroutines waiting for a Fix slot and holding one.
func (d *Dispatcher) FixQueueStats() (waiting, inflight int) {
	return int(d.fixWaiting.Load()), int(d.fixInflight.Load())
}

func (d *Dispatcher) repoFixSem(repo string) chan struct{} {
	d.fixRepoMu.Lock()
	defer d.fixRepoMu.Unlock()
	if d.fixRepoSem == nil {
		d.fixRepoSem = map[string]chan struct{}{}
	}
	ch, ok := d.fixRepoSem[repo]
	if !ok {
		ch = make(chan struct{}, 1)
		d.fixRepoSem[repo] = ch
	}
	return ch
}

// Fix runs a per-repo subagent with the failure evidence, expecting it
// to commit+push a fix or leave a note in BACKLOG.md if it can't.
func (d *Dispatcher) Fix(ctx context.Context, s orchestrator.Signal) error {
	d.fixWaiting.Add(1)
	if d.Metrics != nil {
		d.Metrics.FixQueueDepth.Add(ctx, 1)
	}
	waitStart := time.Now()
	release, err := d.acquireFixSlot(ctx, s.Repo)
	wait := time.Since(waitStart)
	d.fixWaiting.Add(-1)
	if err != nil {
		if d.Metrics != nil {
			d.Metrics.FixQueueDepth.Add(ctx, -1)
			d.Metrics.FixQueueWait.Record(ctx, wait.Seconds())
		}
		return err
	}
	d.fixInflight.Add(1)
	if d.Metrics != nil {
		d.Metrics.FixQueueWait.Record(ctx, wait.Seconds())
	}
	defer func() {
		release()
		d.fixInflight.Add(-1)
		if d.Metrics != nil {
			d.Metrics.FixQueueDepth.Add(context.Background(), -1)
		}
	}()

	if d.FixBudget > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d.FixBudget)
		defer cancel()
	}

	start := time.Now()
	err = d.fixInner(ctx, s)
	d.observe("fix", start, err)
	return err
}

// acquireFixSlot takes per-repo (cap 1) then global fixSem. release frees both.
func (d *Dispatcher) acquireFixSlot(ctx context.Context, repo string) (release func(), err error) {
	var repoSem chan struct{}
	if d.fixSem != nil {
		repoSem = d.repoFixSem(repo)
		select {
		case repoSem <- struct{}{}:
		case <-ctx.Done():
			return nil, fmt.Errorf("dispatch: fix: wait repo slot: %w", ctx.Err())
		}
	}
	if d.fixSem != nil {
		select {
		case d.fixSem <- struct{}{}:
		case <-ctx.Done():
			if repoSem != nil {
				<-repoSem
			}
			return nil, fmt.Errorf("dispatch: fix: wait slot: %w", ctx.Err())
		}
	}
	return func() {
		if d.fixSem != nil {
			<-d.fixSem
		}
		if repoSem != nil {
			<-repoSem
		}
	}, nil
}

func (d *Dispatcher) fixInner(ctx context.Context, s orchestrator.Signal) error {
	if err := d.Repos.EnsureCloned(ctx, s.Repo); err != nil {
		return fmt.Errorf("dispatch: fix: %w", err)
	}
	dir := d.Repos.Dir(s.Repo)
	evidence := s.Evidence
	if s.Source == orchestrator.SourceCI && d.FetchLogs != nil {
		if runURL, _ := evidence["run_url"].(string); runURL != "" {
			if logs, err := d.FetchLogs(ctx, runURL); err != nil {
				d.Log.Warn("ci log fetch failed", "repo", s.Repo, "error", err)
			} else if logs != "" {
				evidence = copyEvidence(evidence)
				evidence["logs"] = logs
			}
		}
	}
	reason := fmt.Sprintf("%s reported %s", s.Source, s.Kind)
	var prBranch string
	if d.FixMode == "pr" {
		// Generated here, not by the subagent, so FindPR can look the
		// resulting PR up afterward without parsing CLI output for a URL.
		prBranch = fmt.Sprintf("xdlc-fix-%d", time.Now().UnixNano())
	}
	teamRules := subagent.ReadTeamInstructions(dir)
	if extra := d.Repos.AgentInstructions(s.Repo); extra != "" {
		if teamRules != "" {
			teamRules += "\n\n"
		}
		teamRules += "config agent_instructions:\n" + strings.TrimSpace(extra)
	}

	runner := d.Subagent
	provider := d.DefaultProvider
	if provider == "" {
		provider = string(subagent.ProviderClaude)
	}
	if s.OperatorAgentProvider != "" {
		provider = s.OperatorAgentProvider
		if d.NewRunner != nil {
			runner = d.NewRunner(provider)
		} else {
			runner = subagent.NewSubprocessRunner(subagent.Provider(provider), "", nil, 0, nil)
		}
	} else if d.Route == "cheapest" && len(d.Providers) > 0 {
		var stats map[string]ProviderStats
		if d.ProviderStats != nil {
			stats = d.ProviderStats()
		}
		provider = PickProvider(d.Route, d.DefaultProvider, d.Providers, d.RouteMinSuccess, stats)
		if d.NewRunner != nil {
			runner = d.NewRunner(provider)
		}
	}
	if s.Evidence != nil {
		s.Evidence["agent_provider"] = provider
	}

	authEnv := d.Repos.AuthEnv()
	// Console-supplied key: inject once, clear from Signal so it cannot
	// reach audit/backlog if a later path dumps the struct.
	if key := s.OperatorAgentKey; key != "" {
		s.OperatorAgentKey = ""
		envName := subagent.APIKeyEnvName(subagent.Provider(provider))
		authEnv = append(append([]string{}, authEnv...), envName+"="+key)
	}
	lessons := ""
	if d.Lessons != nil {
		lessons = d.Lessons.ForRepo(s.Repo, 5)
	}
	var prompt string
	if d.FixPlan {
		planPrompt := subagent.PlanPrompt(s.Repo, reason, evidence, teamRules)
		subStart := time.Now()
		planOut, perr := runner.Run(ctx, dir, planPrompt, authEnv)
		subagent.MergeCost(s.Evidence, planOut)
		if d.Metrics != nil {
			status := "ok"
			if perr != nil {
				status = "error"
			}
			d.Metrics.SubagentRuns.Record(ctx, time.Since(subStart).Seconds(),
				metric.WithAttributes(otel.AttrStatus(status)))
		}
		d.Log.Info("subagent plan finished", "repo", s.Repo, "output", truncate(planOut, 2000))
		if perr != nil {
			d.recordLesson(s, "error", reason)
			return fmt.Errorf("dispatch: fix: plan: %w", perr)
		}
		if s.Evidence != nil {
			s.Evidence["fix_plan"] = "used"
		}
		prompt = subagent.FixFromPlanPrompt(s.Repo, reason, evidence, d.FixMode, prBranch, teamRules, planOut, lessons)
	} else {
		prompt = subagent.FixPrompt(s.Repo, reason, evidence, d.FixMode, prBranch, teamRules, lessons)
	}

	subStart := time.Now()
	// Inject git AuthEnv (GIT_CONFIG_* http.extraHeader) so the subagent
	// can `git push` without GITHUB_TOKEN in its allowlist — same credential
	// path Promote/Revert use. Never pass App PEM / webhook secrets.
	out, err := runner.Run(ctx, dir, prompt, authEnv)
	// Best-effort cost/tokens into Evidence (audit/backlog), even on error.
	subagent.MergeCost(s.Evidence, out)
	if d.Metrics != nil {
		status := "ok"
		if err != nil {
			status = "error"
		}
		d.Metrics.SubagentRuns.Record(ctx, time.Since(subStart).Seconds(),
			metric.WithAttributes(otel.AttrStatus(status)))
	}
	d.Log.Info("subagent finished", "repo", s.Repo, "output", truncate(out, 2000))
	if err != nil {
		d.recordLesson(s, "error", reason)
		return fmt.Errorf("dispatch: fix: subagent: %w", err)
	}

	if d.FixMode == "pr" && s.Evidence != nil {
		ownerRepo := d.Repos.GitHub(s.Repo)
		base := d.Repos.Branch(s.Repo)
		var pr *PRRef
		if d.FindPR != nil {
			var perr error
			pr, perr = d.FindPR(ctx, ownerRepo, prBranch)
			if perr != nil {
				d.Log.Warn("pr lookup failed", "repo", s.Repo, "branch", prBranch, "error", perr)
			}
		}
		if pr == nil && d.CreatePR != nil {
			title := fmt.Sprintf("xdlc fix: %s", s.Repo)
			body := fmt.Sprintf("Automated Fix for %s (%s).\n\nChain evidence is in BACKLOG.md / audit history.", s.Source, s.Kind)
			created, cerr := d.CreatePR(ctx, ownerRepo, prBranch, base, title, body)
			if cerr != nil {
				d.Log.Warn("pr create failed", "repo", s.Repo, "branch", prBranch, "error", cerr)
			} else {
				pr = created
			}
		}
		if pr != nil {
			s.Evidence["pr_number"] = pr.Number
			s.Evidence["pr_url"] = pr.URL
			s.Evidence["pr_state"] = pr.State
			s.Evidence["pr_branch"] = prBranch
		} else {
			d.Log.Warn("fix_mode pr: no PR after subagent run", "repo", s.Repo, "branch", prBranch)
		}
	}

	if d.Reverify != nil {
		if rerr := d.Reverify(ctx, s); rerr != nil {
			if s.Evidence != nil {
				s.Evidence["escalate"] = "reverify_failed"
			}
			d.recordLesson(s, "error", reason+": reverify failed")
			return fmt.Errorf("dispatch: fix: reverify: %w", rerr)
		}
		if s.Evidence != nil {
			s.Evidence["reverify"] = "pass"
		}
	}
	d.recordLesson(s, "ok", reason)
	return nil
}

func (d *Dispatcher) recordLesson(s orchestrator.Signal, outcome, symptom string) {
	if d.Lessons == nil {
		return
	}
	if err := d.Lessons.Record(s.Repo, string(s.Source), outcome, symptom); err != nil {
		d.Log.Warn("lesson record failed", "repo", s.Repo, "error", err)
	}
}

func (d *Dispatcher) observe(action string, start time.Time, err error) {
	if d.Metrics == nil {
		return
	}
	status := "ok"
	if err != nil {
		status = "error"
	}
	d.Metrics.Dispatch.Record(context.Background(), time.Since(start).Seconds(),
		metric.WithAttributes(otel.AttrAction(action), otel.AttrStatus(status)))
}

func copyEvidence(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Revert reverts HEAD on the prod branch and pushes, then aligns the
// dev branch when it still pointed at the pre-revert prod tip.
// Rollback-first for prod-health breaches.
//
// Unlike Promote, this deliberately does *not* pin to Signal.SHA, and
// prod-health signals carry none. A breach says "prod is unhealthy
// now"; the thing to undo is prod's current tip, not whatever commit
// happened to be deployed when the alert first fired — pinning would
// make the rollback refuse exactly when prod has changed most. The race
// is covered differently: this re-reads origin/<prod> and pushes without
// --force, so if prod moved under us the push is rejected and the revert
// fails loudly rather than clobbering it.
func (d *Dispatcher) Revert(ctx context.Context, s orchestrator.Signal) error {
	start := time.Now()
	err := d.revertInner(ctx, s)
	d.observe("revert", start, err)
	return err
}

func (d *Dispatcher) revertInner(ctx context.Context, s orchestrator.Signal) error {
	if err := d.Repos.EnsureCloned(ctx, s.Repo); err != nil {
		return fmt.Errorf("dispatch: revert: %w", err)
	}
	dir := d.Repos.Dir(s.Repo)
	env := d.Repos.AuthEnv()
	dev := d.Repos.Branch(s.Repo)
	prod := d.Repos.ProdBranch(s.Repo)

	run := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...) //nolint:gosec
		if len(env) > 0 {
			cmd.Env = append(os.Environ(), env...)
		}
		return cmd.CombinedOutput()
	}

	if out, err := run("fetch", "origin", prod, dev); err != nil {
		return fmt.Errorf("dispatch: revert: fetch: %w: %s", err, out)
	}
	oldProd, err := run("rev-parse", "origin/"+prod)
	if err != nil {
		return fmt.Errorf("dispatch: revert: rev-parse %s: %w: %s", prod, err, oldProd)
	}
	oldProdSHA := strings.TrimSpace(string(oldProd))

	steps := [][]string{
		{"checkout", prod},
		{"reset", "--hard", "origin/" + prod},
		{"revert", "--no-edit", "HEAD"},
		{"push", "origin", prod},
	}
	for _, args := range steps {
		if out, err := run(args...); err != nil {
			return fmt.Errorf("dispatch: revert: git %v: %w: %s", args, err, out)
		}
	}

	// Keep dev aligned when it was at the same tip as pre-revert prod
	// (normal right after a promote). Otherwise leave dev alone —
	// cherry-pick would be a separate policy.
	devSHA, err := run("rev-parse", "origin/"+dev)
	if err != nil {
		return fmt.Errorf("dispatch: revert: rev-parse %s: %w: %s", dev, err, devSHA)
	}
	if strings.TrimSpace(string(devSHA)) == oldProdSHA {
		if out, err := run("push", "origin", prod+":"+dev); err != nil {
			return fmt.Errorf("dispatch: revert: align %s: %w: %s", dev, err, out)
		}
	}

	d.Log.Info("reverted", "repo", s.Repo, "branch", prod)
	return nil
}

// Promote copies the gated image tag into values/prod (when gitops/ is
// present), then fast-forwards the gated commit onto prod.
//
// The Signal's SHA is the whole point: a dev-gate pass is a verdict on
// one commit, and dispatch can run minutes later. Promoting the *branch*
// would ship everything that landed in between under that commit's
// passing probe. So the promote is pinned — origin/<dev> must still be
// the gated commit, and that object is what gets pushed. A Signal with
// no SHA (an operator's manual promote) keeps the old branch-tip
// behavior, with a warning, since a human authorized that one.
func (d *Dispatcher) Promote(ctx context.Context, s orchestrator.Signal) error {
	start := time.Now()
	err := d.promoteInner(ctx, s)
	d.observe("promote", start, err)
	return err
}

func (d *Dispatcher) promoteInner(ctx context.Context, s orchestrator.Signal) error {
	if err := d.Repos.EnsureCloned(ctx, s.Repo); err != nil {
		return fmt.Errorf("dispatch: promote: %w", err)
	}
	dir := d.Repos.Dir(s.Repo)
	env := d.Repos.AuthEnv()
	dev := d.Repos.Branch(s.Repo)
	prod := d.Repos.ProdBranch(s.Repo)

	pin := s.SHA
	if pin == "" {
		d.Log.Warn("promote is not pinned to a gated commit; pushing current branch tip",
			"repo", s.Repo, "source", s.Source, "branch", dev)
	} else if err := promote.VerifyRemoteTip(ctx, dir, env, dev, pin); err != nil {
		// Checked before the tag carry so a moved branch costs nothing.
		return fmt.Errorf("dispatch: promote: %w", err)
	}

	changed, err := promote.CarryProdTag(dir, s.Repo)
	if err != nil {
		return fmt.Errorf("dispatch: promote: %w", err)
	}
	if changed {
		carried, err := promote.CommitProdTag(ctx, dir, s.Repo, env, dev)
		if err != nil {
			return fmt.Errorf("dispatch: promote: %w", err)
		}
		// The carry commit is now the dev tip. It is ours and it is a
		// direct child of the verified commit, so re-pin to it rather
		// than falling back to an unpinned branch push.
		if pin != "" && carried != "" {
			pin = carried
		}
	}
	if err := promote.FastForward(ctx, dir, env, dev, prod, pin); err != nil {
		return fmt.Errorf("dispatch: promote: %w", err)
	}
	d.Log.Info("promoted", "repo", s.Repo, "from", dev, "to", prod, "sha", pin)
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}
