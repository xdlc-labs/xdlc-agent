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
	"github.com/xdlc-labs/xdlc-agent/internal/session"
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
	// RulesFile is config.yaml's agent.rules_file — a daemon-wide
	// instructions file appended to every Fix prompt's trusted block.
	RulesFile string
	// Sessions records each Fix's prompt, output and diff to disk.
	// nil (or a nil *session.Store) disables recording.
	Sessions *session.Store
	// Lessons optional past-Fix inject (issue #19). nil skips.
	Lessons interface {
		Record(repo, source, outcome, symptom string) error
		ForRepo(repo string, k int) string
	}
	// Reverify, when set, is called after a successful Fix subagent run
	// (issue #2). Non-nil error means the gate is still red — Fix fails,
	// or, when FixAttempts allows it, the agent is run again.
	//
	// It returns the gate's own evidence from that re-check (a fresh
	// run_url and conclusion for CI) so a retry can be told about the
	// run that is red *now*, not the one that triggered the Fix minutes
	// ago. Nil evidence is fine; the retry then reuses what it had.
	Reverify func(ctx context.Context, s orchestrator.Signal) (map[string]any, error)
	// Worktree runs each Fix in its own git worktree on a scratch branch
	// instead of the repo's shared clone, and pushes the result from the
	// daemon rather than from the agent. See repos.Worktree for why.
	Worktree bool
	// WorktreeKeepFailed is how long a failed Fix's worktree is left on
	// disk for inspection. 0 → repos.DefaultKeepFailed.
	WorktreeKeepFailed time.Duration
	// FixAttempts caps how many times the coding agent is run for one
	// signal. 0 or 1 is the single-shot behavior. Values above 1 need
	// Reverify — without a gate re-check nothing can say the first
	// attempt failed, so the ladder is clamped back to 1.
	FixAttempts int
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
	FixBudget   time.Duration
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
//
// The per-repo cap exists only because Fixes used to share one clone and
// would edit each other's files. With a worktree per Fix that is no
// longer true, so worktree mode takes the global cap alone and two
// signals for one repo can be fixed at once.
func (d *Dispatcher) acquireFixSlot(ctx context.Context, repo string) (release func(), err error) {
	var repoSem chan struct{}
	if d.fixSem != nil && !d.Worktree {
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

// SetWorktree turns per-Fix worktrees on and sets how long a failed run's
// worktree is kept. Call before Fix.
func (d *Dispatcher) SetWorktree(enabled bool, keepFailed time.Duration) {
	d.Worktree = enabled
	d.WorktreeKeepFailed = keepFailed
}

func (d *Dispatcher) fixInner(ctx context.Context, s orchestrator.Signal) (err error) {
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

	// Session recording: the prompt the agent got, everything it printed,
	// and the patch it left behind. Best-effort throughout — a recorder
	// failure must never fail a Fix that otherwise worked.
	//
	// It is started before the worktree because the session id names the
	// worktree: one run, one name, in the recording and on disk. The
	// store allocates that id by creating the directory, so two Fixes
	// starting in the same second cannot be handed the same one — which
	// matters now that two Fixes for one repo really can overlap.
	var sess *session.Session
	manual, _ := s.Evidence["manual"].(bool)
	if d.Sessions != nil {
		started, serr := d.Sessions.Start(session.Meta{
			Repo:     s.Repo,
			Source:   string(s.Source),
			Kind:     string(s.Kind),
			Provider: provider,
			FixMode:  d.FixMode,
			Manual:   manual,
		})
		if serr != nil {
			d.Log.Warn("session start failed", "repo", s.Repo, "error", serr)
		} else {
			sess = started
			if s.Evidence != nil && sess.ID() != "" {
				s.Evidence["session_id"] = sess.ID()
			}
		}
	}
	runID := sess.ID()
	if runID == "" {
		runID = newRunID(s.Repo)
	}

	// Per-Fix worktree: the agent gets its own checkout on its own scratch
	// branch, so a second Fix for this repo can run beside it and a run
	// killed mid-edit cannot leave the shared clone dirty.
	branch := d.Repos.Branch(s.Repo)
	if prBranch != "" {
		branch = prBranch
	}
	var wt *repos.Worktree
	if d.Worktree {
		d.pruneWorktrees(ctx, s.Repo)
		created, werr := d.Repos.Worktree(ctx, s.Repo, runID)
		if werr != nil {
			return fmt.Errorf("dispatch: fix: %w", werr)
		}
		wt = created
		dir = wt.Dir
		branch = wt.Branch
		defer d.releaseWorktree(ctx, s.Repo, wt, &err)
	}
	baseSHA := session.HeadSHA(ctx, dir)
	sess.SetGit(baseSHA, "", branch, 0)

	teamRules := subagent.ReadTeamInstructions(dir, d.RulesFile)
	if extra := d.Repos.AgentInstructions(s.Repo); extra != "" {
		if teamRules != "" {
			teamRules += "\n\n"
		}
		teamRules += "config agent_instructions:\n" + strings.TrimSpace(extra)
	}
	// Operator's free-text goal from the console / API, last so it wins
	// a conflict with the standing repo rules. Trusted: it comes from an
	// authenticated operator, not from gate evidence.
	if extra := strings.TrimSpace(s.OperatorInstructions); extra != "" {
		if teamRules != "" {
			teamRules += "\n\n"
		}
		teamRules += "operator instructions for this run:\n" + extra
	}

	defer func() {
		if sess == nil {
			return
		}
		// The Fix context may already be canceled (timeout / budget);
		// the recording still has to be written.
		dctx := context.WithoutCancel(ctx)
		patch, changed := session.Diff(dctx, dir, baseSHA)
		if werr := sess.Write(session.FileDiff, patch); werr != nil {
			d.Log.Warn("session diff write failed", "repo", s.Repo, "error", werr)
		}
		sess.SetGit(baseSHA, session.HeadSHA(dctx, dir), branch, changed)
		status := "ok"
		msg := ""
		if err != nil {
			status, msg = "error", err.Error()
		}
		sess.SetResult(status, msg, costFields(s.Evidence))
		if ferr := sess.Finish(); ferr != nil {
			d.Log.Warn("session finish failed", "repo", s.Repo, "error", ferr)
		}
	}()

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

	// The diagnose pass runs once, ahead of the ladder: its subject is
	// the failure, not any one patch attempt, so re-planning per attempt
	// would pay for the same diagnosis twice.
	plan := ""
	if d.FixPlan {
		plan, err = d.runPlanPass(ctx, s, sess, dir, reason, evidence, teamRules, runner, authEnv)
		if err != nil {
			d.recordLesson(s, "error", reason)
			return err
		}
	}

	attempts := d.FixAttempts
	if attempts < 1 {
		attempts = 1
	}
	if d.Reverify == nil && attempts > 1 {
		// Nothing would tell attempt 1 that it failed, so every extra
		// attempt would re-run a fix that already reported success.
		d.Log.Warn("fix_attempts > 1 without fix_reverify; running once",
			"repo", s.Repo, "fix_attempts", attempts)
		attempts = 1
	}

	var (
		fixErr   error
		verdict  subagent.Verdict
		retry    *subagent.RetryContext
		attempt  int
		ranAgent bool
	)

	for attempt = 1; ; attempt++ {
		prompt := subagent.BuildFixPrompt(subagent.FixRequest{
			Repo:      s.Repo,
			Reason:    reason,
			Evidence:  evidence,
			Mode:      d.FixMode,
			PRBranch:  prBranch,
			TeamRules: teamRules,
			Lessons:   lessons,
			Plan:      plan,
			Retry:     retry,
			NoPush:    wt != nil,
		})
		if werr := sess.Write(session.AttemptFile(session.FilePrompt, attempt), prompt); werr != nil {
			d.Log.Warn("session prompt write failed", "repo", s.Repo, "error", werr)
		}

		subStart := time.Now()
		// Inject git AuthEnv (GIT_CONFIG_* http.extraHeader) so the subagent
		// can `git push` without GITHUB_TOKEN in its allowlist — same credential
		// path Promote/Revert use. Never pass App PEM / webhook secrets.
		out, runErr := runner.Run(ctx, dir, prompt, authEnv)
		ranAgent = true
		// Best-effort cost/tokens into Evidence (audit/backlog), even on
		// error. Summed, not replaced: a Fix that took three attempts
		// cost all three, and the audit row is what an operator judges
		// the ladder by.
		subagent.AddCost(s.Evidence, out)
		if d.Metrics != nil {
			status := "ok"
			if runErr != nil {
				status = "error"
			}
			d.Metrics.SubagentRuns.Record(ctx, time.Since(subStart).Seconds(),
				metric.WithAttributes(otel.AttrStatus(status)))
		}
		verdict = subagent.ParseVerdict(out)
		d.Log.Info("subagent finished", "repo", s.Repo, "attempt", attempt,
			"outcome", string(verdict.Outcome), "summary", verdict.Summary,
			"output", truncate(out, 2000))
		if werr := sess.Write(session.AttemptFile(session.FileOutput, attempt), out); werr != nil {
			d.Log.Warn("session output write failed", "repo", s.Repo, "error", werr)
		}
		recordVerdict(s.Evidence, verdict)

		if runErr != nil {
			fixErr = fmt.Errorf("dispatch: fix: subagent: %w", runErr)
			break
		}

		// In worktree mode the agent only ever commits; the push is the
		// daemon's, so it happens here — before the gate re-check, which
		// has nothing to look at until the code is on the branch.
		if wt != nil {
			target := d.Repos.Branch(s.Repo)
			if prBranch != "" {
				target = prBranch
			}
			if perr := d.pushWorktree(ctx, s, wt, target); perr != nil {
				fixErr = perr
				break
			}
		}

		// The agent declared itself blocked. Both outcomes exit 0, so
		// without the verdict this would have been recorded as a clean
		// Fix and the failure would sit unnoticed behind a green row.
		if !verdict.Retryable() {
			if s.Evidence != nil {
				s.Evidence["escalate"] = verdict.Escalation()
			}
			fixErr = fmt.Errorf("dispatch: fix: agent reported %s: %s",
				verdict.Outcome, verdict.Summary)
			break
		}

		if d.Reverify == nil {
			break
		}
		revEvidence, rerr := d.Reverify(ctx, s)
		if rerr == nil {
			if s.Evidence != nil {
				s.Evidence["reverify"] = "pass"
			}
			break
		}
		if attempt >= attempts {
			if s.Evidence != nil {
				s.Evidence["escalate"] = "reverify_failed"
			}
			fixErr = fmt.Errorf("dispatch: fix: reverify: %w", rerr)
			break
		}

		d.Log.Info("gate still red after fix; retrying", "repo", s.Repo,
			"attempt", attempt, "of", attempts, "error", rerr)
		if d.Metrics != nil && d.Metrics.FixRetries != nil {
			d.Metrics.FixRetries.Add(ctx, 1)
		}
		retry = &subagent.RetryContext{
			Attempt:     attempt + 1,
			MaxAttempts: attempts,
			PrevSummary: verdict.Summary,
			GateFailure: rerr.Error(),
		}
		evidence = d.refreshEvidence(ctx, s.Repo, evidence, revEvidence)
	}

	if s.Evidence != nil && attempt > 1 {
		s.Evidence["fix_attempts"] = attempt
	}
	sess.SetVerdict(string(verdict.Outcome), verdict.Summary, attempt)

	// PR bookkeeping runs on the failure path too: a pr-mode Fix that
	// could not turn the gate green still pushed a branch, and the
	// operator reviewing it needs the PR link either way.
	if ranAgent && d.FixMode == "pr" && s.Evidence != nil {
		d.finishPR(ctx, s, sess, prBranch)
	}

	if fixErr != nil {
		d.recordLesson(s, "error", lessonSymptom(reason, verdict, fixErr))
		return fixErr
	}
	d.recordLesson(s, "ok", lessonSymptom(reason, verdict, nil))
	return nil
}

// runPlanPass executes the diagnose-only first pass and returns the plan
// text for BuildFixPrompt to implement.
func (d *Dispatcher) runPlanPass(
	ctx context.Context,
	s orchestrator.Signal,
	sess *session.Session,
	dir, reason string,
	evidence map[string]any,
	teamRules string,
	runner subagent.Runner,
	authEnv []string,
) (string, error) {
	planPrompt := subagent.PlanPrompt(s.Repo, reason, evidence, teamRules)
	if werr := sess.Write(session.FilePrompt, planPrompt); werr != nil {
		d.Log.Warn("session prompt write failed", "repo", s.Repo, "error", werr)
	}
	subStart := time.Now()
	planOut, perr := runner.Run(ctx, dir, planPrompt, authEnv)
	// Summed with the patch pass that follows: both runs are billed.
	subagent.AddCost(s.Evidence, planOut)
	if d.Metrics != nil {
		status := "ok"
		if perr != nil {
			status = "error"
		}
		d.Metrics.SubagentRuns.Record(ctx, time.Since(subStart).Seconds(),
			metric.WithAttributes(otel.AttrStatus(status)))
	}
	d.Log.Info("subagent plan finished", "repo", s.Repo, "output", truncate(planOut, 2000))
	if werr := sess.Write(session.FilePlan, planOut); werr != nil {
		d.Log.Warn("session plan write failed", "repo", s.Repo, "error", werr)
	}
	if perr != nil {
		return "", fmt.Errorf("dispatch: fix: plan: %w", perr)
	}
	if s.Evidence != nil {
		s.Evidence["fix_plan"] = "used"
	}
	return planOut, nil
}

// finishPR looks up (or opens) the PR a pr-mode Fix pushed, recording it
// on the signal and the session.
func (d *Dispatcher) finishPR(ctx context.Context, s orchestrator.Signal, sess *session.Session, prBranch string) {
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
	if pr == nil {
		d.Log.Warn("fix_mode pr: no PR after subagent run", "repo", s.Repo, "branch", prBranch)
		return
	}
	sess.SetPR(pr.URL)
	s.Evidence["pr_number"] = pr.Number
	s.Evidence["pr_url"] = pr.URL
	s.Evidence["pr_state"] = pr.State
	s.Evidence["pr_branch"] = prBranch
}

// refreshEvidence folds the re-check's gate evidence into what the next
// attempt is shown, then re-fetches CI logs for the run that is failing
// now. Without this a retry would be handed the logs of the run that
// triggered the original Fix — output the agent has already read and
// already acted on.
func (d *Dispatcher) refreshEvidence(ctx context.Context, repo string, prev, gate map[string]any) map[string]any {
	if len(gate) == 0 {
		return prev
	}
	next := copyEvidence(prev)
	for k, v := range gate {
		next[k] = v
	}
	if runURL, _ := gate["run_url"].(string); runURL != "" && d.FetchLogs != nil {
		if logs, err := d.FetchLogs(ctx, runURL); err != nil {
			d.Log.Warn("retry log fetch failed", "repo", repo, "error", err)
		} else if logs != "" {
			next["logs"] = logs
		}
	}
	return next
}

// recordVerdict copies the agent's self-report into the signal evidence
// that reaches BACKLOG.md, the audit store and the console.
func recordVerdict(evidence map[string]any, v subagent.Verdict) {
	if evidence == nil || !v.Found() {
		return
	}
	evidence["agent_outcome"] = string(v.Outcome)
	if v.Summary != "" {
		evidence["agent_summary"] = v.Summary
	}
}

// lessonSymptom builds the LESSONS.md line body. The agent's own summary
// is the useful half — "ci reported fail" repeated on every row taught
// the next run nothing.
func lessonSymptom(reason string, v subagent.Verdict, err error) string {
	parts := []string{reason}
	if v.Found() {
		parts = append(parts, "agent="+string(v.Outcome))
	}
	if v.Summary != "" {
		parts = append(parts, v.Summary)
	}
	if err != nil {
		parts = append(parts, "failed: "+err.Error())
	}
	return strings.Join(parts, "; ")
}

// runIDSeq disambiguates run ids generated without a session store,
// which would otherwise collide for two Fixes started in the same second.
var runIDSeq atomic.Int64

// newRunID names a Fix run when session recording is off. With recording
// on the session store allocates the id instead, so the worktree and the
// recording share one name.
func newRunID(repo string) string {
	return fmt.Sprintf("%s-%d", session.NewID(time.Now().UTC(), repo), runIDSeq.Add(1))
}

// pruneWorktrees sweeps this repo's worktrees left behind by earlier
// failed runs. Best-effort: a sweep failure must not stop a Fix.
func (d *Dispatcher) pruneWorktrees(ctx context.Context, repo string) {
	n, err := d.Repos.PruneWorktrees(ctx, repo, d.WorktreeKeepFailed)
	if err != nil {
		d.Log.Warn("worktree prune failed", "repo", repo, "error", err)
		return
	}
	if n > 0 {
		d.Log.Info("pruned stale fix worktrees", "repo", repo, "count", n)
	}
}

// releaseWorktree removes the worktree after a successful Fix, and keeps
// it after a failed one so an operator can see what the agent actually
// left behind. errp is the named return of fixInner, read at defer time.
func (d *Dispatcher) releaseWorktree(ctx context.Context, repo string, wt *repos.Worktree, errp *error) {
	if wt == nil {
		return
	}
	if errp != nil && *errp != nil {
		keep := d.WorktreeKeepFailed
		if keep == 0 {
			keep = repos.DefaultKeepFailed
		}
		// Hand it back to the sweep: kept on disk, but no longer shielded
		// as a running Fix, so it is collected once it ages out.
		d.Repos.Done(wt)
		d.Log.Info("keeping worktree of failed fix for inspection",
			"repo", repo, "dir", wt.Dir, "branch", wt.Branch, "keep_for", keep)
		return
	}
	// The Fix context may already be canceled; cleanup still has to run.
	if rerr := d.Repos.Remove(context.WithoutCancel(ctx), wt); rerr != nil {
		d.Log.Warn("worktree remove failed", "repo", repo, "dir", wt.Dir, "error", rerr)
	}
}

// pushWorktree sends the agent's commits to the branch this Fix targets.
// Nothing committed means nothing to push — that is not an error here,
// because an agent that decided the failure was unfixable is expected to
// leave the tree alone, and the verdict is what reports that.
func (d *Dispatcher) pushWorktree(ctx context.Context, s orchestrator.Signal, wt *repos.Worktree, target string) error {
	if wt == nil {
		return nil
	}
	if !d.Repos.HasCommits(ctx, wt) {
		d.Log.Info("fix produced no commits; nothing to push", "repo", s.Repo, "branch", wt.Branch)
		return nil
	}
	if err := d.Repos.Push(ctx, wt, target); err != nil {
		return fmt.Errorf("dispatch: fix: %w", err)
	}
	d.Log.Info("pushed fix", "repo", s.Repo, "from", wt.Branch, "to", target)
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

// costFields pulls the cost/usage keys AddCost wrote into evidence,
// for the session's meta.json. Nil when the provider reported none.
func costFields(evidence map[string]any) map[string]any {
	if evidence == nil {
		return nil
	}
	out := map[string]any{}
	for _, k := range []string{"total_cost_usd", "input_tokens", "output_tokens", "duration_ms"} {
		if v, ok := evidence[k]; ok {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
