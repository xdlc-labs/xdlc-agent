// Package oneshot runs a single CI Fix from a GitHub Actions run URL —
// no daemon, no webhook, no config.yaml. It is the shortest path from
// "CI is red" to "there is a PR": `xdlc fix <run-url>`.
//
// It reuses the daemon's dispatcher end to end (worktree per Fix, the
// same prompt, the same session recording), so a Fix that works here
// works the same way once the daemon is installed.
package oneshot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/config"
	"github.com/xdlc-labs/xdlc-agent/internal/dispatch"
	"github.com/xdlc-labs/xdlc-agent/internal/ghclient"
	"github.com/xdlc-labs/xdlc-agent/internal/mcpserver"
	"github.com/xdlc-labs/xdlc-agent/internal/orchestrator"
	"github.com/xdlc-labs/xdlc-agent/internal/repos"
	"github.com/xdlc-labs/xdlc-agent/internal/session"
	"github.com/xdlc-labs/xdlc-agent/internal/subagent"
)

// GitHub is the slice of ghclient.Client a one-shot Fix talks to.
type GitHub interface {
	GetRun(ctx context.Context, runURL string) (ghclient.Run, error)
	FetchFailedJobLogs(ctx context.Context, runURL string) (string, error)
	FetchAllFailedJobLogs(ctx context.Context, runURL string) ([]ghclient.JobLog, error)
	FindPRByBranch(ctx context.Context, repo, branch string) (*ghclient.PRRef, error)
	CreatePR(ctx context.Context, repo, head, base, title, body string) (*ghclient.PRRef, error)
}

// Options configure Run.
type Options struct {
	RunURL       string        // https://github.com/<owner>/<repo>/actions/runs/<id>
	Provider     string        // claude | codex | cursor | gemini (default claude)
	Model        string        // passed to the agent CLI as --model; empty = the CLI's own default
	Mode         string        // pr (default) | direct
	WorkDir      string        // clones + sessions live here; default os.UserCacheDir()/xdlc
	Instructions string        // optional operator hint, joins the prompt's trusted block
	Timeout      time.Duration // agent wall clock; default 20m
	// StallTimeout kills the agent when it has printed nothing for this
	// long while still alive. 0 (default) disables the watchdog. Worth
	// setting in CI, where a wedged agent burns billed minutes until the
	// wall clock runs out; see subagent.SubprocessRunner.WithStallTimeout
	// for why opting in also switches the CLI to streaming output.
	StallTimeout time.Duration
	// MCP attaches `xdlc mcp --session <id>` to the agent CLI, so it can
	// pull every failed job's complete logs and earlier Fix records on
	// demand instead of receiving a 32 KB slice. Same server the daemon
	// wires with agent.mcp.enabled; here the session store is the one
	// under WorkDir and there is no config, so prod_metrics and
	// repo_config answer "not available".
	MCP bool
	// MCPBinary is the xdlc executable the agent starts for it. Empty →
	// this process's own executable.
	MCPBinary string
	Out       io.Writer    // progress lines; default os.Stdout
	Log       *slog.Logger // dispatcher log; default: warnings to Out

	// Seams for tests. Zero values mean "the real thing".
	GitHub  GitHub
	Runner  subagent.Runner
	Tokens  ghclient.TokenProvider
	RepoDir string // an existing clone to use instead of cloning from GitHub
}

// Result is what one Fix left behind.
type Result struct {
	Run        ghclient.Run
	SessionID  string
	SessionDir string
	PRURL      string
	Branch     string // branch the Fix landed on (PR head, or the tracked branch in direct mode)
	Delivered  bool
	Outcome    string
	Summary    string
	Evidence   map[string]any
}

// ErrNotRed is returned when the run has nothing to fix.
var ErrNotRed = errors.New("oneshot: run is not a failed run")

// Run performs one Fix. It returns an error when the agent did not
// deliver, so a CI job wrapping it fails visibly.
func Run(ctx context.Context, opts Options) (Result, error) {
	var res Result
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	provider := opts.Provider
	if provider == "" {
		provider = string(subagent.ProviderClaude)
	}
	if !subagent.KnownProvider(provider) {
		return res, fmt.Errorf("oneshot: unknown provider %q (want %s)", provider, providerList())
	}
	mode := opts.Mode
	if mode == "" {
		mode = "pr"
	}
	if mode != "pr" && mode != "direct" {
		return res, fmt.Errorf("oneshot: --mode %q (want pr|direct)", mode)
	}
	if _, _, _, err := ghclient.ParseRunURL(opts.RunURL); err != nil {
		return res, fmt.Errorf("oneshot: %w (want https://github.com/<owner>/<repo>/actions/runs/<id>)", err)
	}

	tokens := opts.Tokens
	if tokens == nil {
		tok, source, err := resolveToken(ctx)
		if err != nil {
			return res, err
		}
		tokens = ghclient.StaticToken(tok)
		_, _ = fmt.Fprintf(out, "github auth: %s\n", source)
	}
	gh := opts.GitHub
	if gh == nil {
		gh = ghclient.NewFromProvider(tokens)
	}

	run, err := gh.GetRun(ctx, opts.RunURL)
	if err != nil {
		return res, err
	}
	res.Run = run
	sha := run.HeadSHA
	if len(sha) > 7 {
		sha = sha[:7]
	}
	_, _ = fmt.Fprintf(out, "run: %s on %s@%s → %s\n", nonEmpty(run.Workflow, "workflow"), run.HeadBranch, sha, nonEmpty(run.Conclusion, run.Status))
	if run.Status != "completed" {
		return res, fmt.Errorf("%w: status is %q; wait for it to finish", ErrNotRed, run.Status)
	}
	switch run.Conclusion {
	case "failure", "timed_out":
	default:
		return res, fmt.Errorf("%w: conclusion is %q", ErrNotRed, run.Conclusion)
	}
	if run.HeadBranch == "" {
		return res, errors.New("oneshot: run has no head branch (a workflow_dispatch on a tag?)")
	}

	runner := opts.Runner
	if runner == nil {
		timeout := opts.Timeout
		if timeout <= 0 {
			timeout = 20 * time.Minute
		}
		sub := subagent.NewSubprocessRunner(subagent.Provider(provider), "", nil, timeout, nil)
		if _, err := exec.LookPath(sub.Binary); err != nil {
			return res, fmt.Errorf("oneshot: provider %s: %q not on PATH — install it, or pick another with --provider (%s)", provider, sub.Binary, providerList())
		}
		runner = sub.WithModel(strings.TrimSpace(opts.Model)).WithStallTimeout(opts.StallTimeout)
	}

	workdir := opts.WorkDir
	if workdir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			base = os.TempDir()
		}
		workdir = filepath.Join(base, "xdlc")
	}
	if err := os.MkdirAll(workdir, 0o750); err != nil {
		return res, fmt.Errorf("oneshot: workdir: %w", err)
	}
	ownerRepo := run.Owner + "/" + run.Name
	repoDir := opts.RepoDir
	if repoDir == "" {
		repoDir = filepath.Join(workdir, "repos", run.Owner, run.Name)
	}
	mgr := repos.NewManager(filepath.Join(workdir, "repos"), []config.Repo{{
		Name:   run.Name,
		GitHub: ownerRepo,
		Dir:    repoDir,
		Branch: run.HeadBranch,
	}}, tokens)

	log := opts.Log
	if log == nil {
		log = slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelWarn}))
	}
	disp := dispatch.New(mgr, runner, log)
	disp.DefaultProvider = provider
	disp.FixMode = mode
	disp.SetWorktree(true, 0)
	disp.FetchLogs = gh.FetchFailedJobLogs
	if mode == "pr" {
		disp.FindPR = func(ctx context.Context, repo, branch string) (*dispatch.PRRef, error) {
			pr, err := gh.FindPRByBranch(ctx, repo, branch)
			if err != nil || pr == nil {
				return nil, err
			}
			return &dispatch.PRRef{Number: pr.Number, URL: pr.URL, State: pr.State}, nil
		}
		disp.CreatePR = func(ctx context.Context, repo, head, base, title, body string) (*dispatch.PRRef, error) {
			pr, err := gh.CreatePR(ctx, repo, head, base, title, body)
			if err != nil || pr == nil {
				return nil, err
			}
			return &dispatch.PRRef{Number: pr.Number, URL: pr.URL, State: pr.State}, nil
		}
	}
	sessionDir := filepath.Join(workdir, "sessions")
	sessions, err := session.Open(sessionDir, 0, 0)
	if err != nil {
		return res, fmt.Errorf("oneshot: sessions: %w", err)
	}
	disp.Sessions = sessions
	// The one-shot path keeps its recordings in the user cache dir, so a
	// second `xdlc fix` on the same repo can read what the first tried.
	disp.PriorFixes = config.DefaultPriorFixes
	res.SessionDir = sessionDir
	if opts.MCP {
		bin := opts.MCPBinary
		if bin == "" {
			exe, err := os.Executable()
			if err != nil {
				return res, fmt.Errorf("oneshot: --mcp: cannot locate the xdlc binary: %w", err)
			}
			bin = exe
		}
		sessAbs, err := filepath.Abs(sessionDir)
		if err != nil {
			return res, err
		}
		disp.MCP = &dispatch.MCPSetup{Binary: bin, SessionsDir: sessAbs}
		disp.FetchAllLogs = func(ctx context.Context, runURL string) ([]mcpserver.JobLog, error) {
			jobs, err := gh.FetchAllFailedJobLogs(ctx, runURL)
			out := make([]mcpserver.JobLog, 0, len(jobs))
			for _, j := range jobs {
				out = append(out, mcpserver.JobLog{Name: j.Name, Conclusion: j.Conclusion, Text: j.Text})
			}
			return out, err
		}
		disp.FetchRun = func(_ context.Context, _ string) (dispatch.RunInfo, error) {
			return dispatch.RunInfo{URL: run.HTMLURL, Workflow: run.Workflow, HeadBranch: run.HeadBranch,
				HeadSHA: run.HeadSHA, Status: run.Status, Conclusion: run.Conclusion}, nil
		}
	}

	sig := orchestrator.Signal{
		Source: orchestrator.SourceCI,
		Kind:   orchestrator.KindFail,
		Repo:   run.Name,
		SHA:    run.HeadSHA,
		At:     time.Now().UTC(),
		Evidence: map[string]any{
			"run_url":       run.HTMLURL,
			"conclusion":    run.Conclusion,
			"head_branch":   run.HeadBranch,
			"head_sha":      run.HeadSHA,
			"workflow_name": run.Workflow,
			"manual":        true,
			"via":           "cli",
		},
		OperatorInstructions: strings.TrimSpace(opts.Instructions),
	}
	if sig.Evidence["run_url"] == "" {
		sig.Evidence["run_url"] = opts.RunURL
	}

	agentLine := provider
	if m := strings.TrimSpace(opts.Model); m != "" {
		agentLine += " (" + m + ")"
	}
	_, _ = fmt.Fprintf(out, "agent: %s, mode: %s, clone: %s\n", agentLine, mode, repoDir)
	_, _ = fmt.Fprintln(out, "fixing… (a real agent usually takes 2–10 minutes)")

	fix, fixErr := disp.Fix(ctx, sig)
	res.Evidence = sig.Evidence
	res.Delivered = fix.Delivered
	res.SessionID, _ = sig.Evidence["session_id"].(string)
	res.PRURL, _ = sig.Evidence["pr_url"].(string)
	res.Outcome, _ = sig.Evidence["agent_outcome"].(string)
	res.Summary, _ = sig.Evidence["agent_summary"].(string)
	if b, _ := sig.Evidence["pr_branch"].(string); b != "" {
		res.Branch = b
	} else if fix.Delivered {
		res.Branch = run.HeadBranch
	}

	if res.Summary != "" {
		_, _ = fmt.Fprintf(out, "agent said: %s\n", res.Summary)
	}
	if cost, ok := sig.Evidence["total_cost_usd"].(float64); ok {
		_, _ = fmt.Fprintf(out, "cost: $%.2f\n", cost)
	}
	if res.SessionID != "" {
		_, _ = fmt.Fprintf(out, "session: xdlc sessions show %s --diff --dir %s\n", res.SessionID, sessionDir)
	}
	if fixErr != nil {
		return res, fixErr
	}
	if !fix.Delivered {
		return res, errors.New("oneshot: agent finished but committed nothing")
	}
	switch {
	case res.PRURL != "":
		_, _ = fmt.Fprintf(out, "PR: %s\n", res.PRURL)
	case res.Branch != "":
		_, _ = fmt.Fprintf(out, "pushed: %s\n", res.Branch)
	}
	return res, nil
}

// resolveToken finds a GitHub token the way a developer expects on a
// laptop: GITHUB_TOKEN if set, else whatever `gh auth token` has.
func resolveToken(ctx context.Context) (token, source string, err error) {
	if t := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); t != "" {
		return t, "GITHUB_TOKEN", nil
	}
	if _, lerr := exec.LookPath("gh"); lerr == nil {
		b, rerr := exec.CommandContext(ctx, "gh", "auth", "token").Output()
		if rerr == nil {
			if t := strings.TrimSpace(string(b)); t != "" {
				return t, "gh auth token", nil
			}
		}
	}
	return "", "", errors.New("oneshot: no GitHub token: set GITHUB_TOKEN or run `gh auth login`")
}

func providerList() string {
	var names []string
	for _, p := range subagent.Providers() {
		names = append(names, string(p))
	}
	return strings.Join(names, "|")
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
