// Package demo runs a zero-infra Fix→Promote→Revert loop (issue #5).
package demo

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/backlog"
	"github.com/xdlc-labs/xdlc-agent/internal/config"
	"github.com/xdlc-labs/xdlc-agent/internal/dispatch"
	"github.com/xdlc-labs/xdlc-agent/internal/gate"
	"github.com/xdlc-labs/xdlc-agent/internal/orchestrator"
	"github.com/xdlc-labs/xdlc-agent/internal/repos"
	"github.com/xdlc-labs/xdlc-agent/internal/session"
	"github.com/xdlc-labs/xdlc-agent/internal/store"
	"github.com/xdlc-labs/xdlc-agent/internal/subagent"
)

const repoName = "demo"

// Options configure Run.
type Options struct {
	Provider string    // fake | claude | codex | cursor | gemini (default fake)
	Scenario string    // ci-red | smoke-red | prod-breach | all (default all)
	WorkDir  string    // empty → MkdirTemp; printed to Out
	Out      io.Writer // live loop lines; default os.Stdout
}

type stepResult struct {
	action orchestrator.Action
	err    error
}

// Run executes the demo loop. Exit-style: nil on success.
func Run(ctx context.Context, opts Options) error {
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	provider := opts.Provider
	if provider == "" {
		provider = "fake"
	}
	scenario := opts.Scenario
	if scenario == "" {
		scenario = "all"
	}

	workdir := opts.WorkDir
	if workdir == "" {
		dir, err := os.MkdirTemp("", "xdlc-demo-*")
		if err != nil {
			return fmt.Errorf("demo: temp dir: %w", err)
		}
		workdir = dir
	}
	_, _ = fmt.Fprintf(out, "demo workdir: %s\n", workdir)

	bareDir, workRepo, err := setupOrigin(workdir)
	if err != nil {
		return err
	}
	_ = bareDir

	bl, err := backlog.Open(filepath.Join(workdir, "BACKLOG.md"))
	if err != nil {
		return err
	}
	auditPath := filepath.Join(workdir, "xdlc-agent-history.db")
	audit, err := store.Open(auditPath)
	if err != nil {
		return err
	}
	defer func() { _ = audit.Close() }()

	runner, err := newRunner(provider)
	if err != nil {
		return err
	}

	mgr := repos.NewManager(workdir, []config.Repo{{
		Name:       repoName,
		GitHub:     "local/demo",
		Dir:        workRepo,
		Branch:     "develop",
		ProdBranch: "main",
	}}, nil)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	disp := dispatch.New(mgr, runner, log)
	disp.DefaultProvider = provider
	// Record the demo Fix like a real one, so `xdlc sessions` has
	// something to show the first time anyone runs it.
	sessionDir := filepath.Join(workdir, "sessions")
	sessions, err := session.Open(sessionDir, 0, 0)
	if err != nil {
		return err
	}
	disp.Sessions = sessions
	// Same default as the daemon, so the demo exercises the path a real
	// install takes: the agent commits in a worktree, xdlc pushes.
	disp.SetWorktree(true, 0)
	// ponytail: in-process CI = go test; no GH Actions
	disp.Reverify = func(ctx context.Context, s orchestrator.Signal) (map[string]any, error) {
		if s.Source != orchestrator.SourceCI {
			return nil, nil
		}
		// The Fix landed on origin, not in this clone — the agent worked
		// in a worktree. Pull the pushed commit down before testing it,
		// which is what a real CI run does when it checks the branch out.
		if err := mgr.EnsureCloned(ctx, s.Repo); err != nil {
			return nil, fmt.Errorf("demo reverify: sync clone: %w", err)
		}
		cmd := exec.CommandContext(ctx, "go", "test", "./...")
		cmd.Dir = mgr.Dir(s.Repo)
		outb, err := cmd.CombinedOutput()
		if err != nil {
			// The test output is the freshest failure there is here, so
			// hand it back as evidence for a retry attempt to read.
			return map[string]any{"logs": string(outb)}, fmt.Errorf("go test: %w: %s", err, outb)
		}
		return nil, nil
	}

	// ponytail: no Argo/Prom — closures stand in; Signals drive the loop
	smoke := &gate.SmokeGate{
		ArgoCDApp: "demo",
		ProbeJob:  "local",
		AppHealthy: func(context.Context, string) (bool, error) {
			return true, nil
		},
		ProbeResult: func(context.Context, string) (bool, string, error) {
			return true, "ok", nil
		},
	}
	var promCalls int
	prod := &gate.ProdHealthGate{
		P95ThresholdMS:  500,
		ErrorRateThresh: 0.01,
		P95Query:        "p95",
		ErrorRateQuery:  "err",
		Query: func(context.Context, string) (float64, error) {
			promCalls++
			// first pair of queries → breach; later → healthy
			if promCalls <= 2 {
				return 999, nil
			}
			return 0, nil
		},
	}

	o := orchestrator.New(disp, bl, log)
	results := make(chan stepResult, 8)
	o.Audit = func(s orchestrator.Signal, action orchestrator.Action, dispatchErr error, started time.Time) error {
		status := store.StatusOK
		errMsg := ""
		if dispatchErr != nil {
			status = store.StatusError
			errMsg = dispatchErr.Error()
		}
		_, _ = fmt.Fprintf(out, "signal=%s/%s action=%s status=%s\n", s.Source, s.Kind, action, status)
		select {
		case results <- stepResult{action: action, err: dispatchErr}:
		default:
		}
		provider := ""
		if s.Evidence != nil {
			if v, ok := s.Evidence["agent_provider"].(string); ok {
				provider = v
			}
		}
		return audit.Append(store.Record{
			At:            time.Now().UTC(),
			Repo:          s.Repo,
			Source:        string(s.Source),
			Kind:          string(s.Kind),
			Action:        string(action),
			Status:        status,
			Error:         errMsg,
			DurationMS:    time.Since(started).Milliseconds(),
			AgentProvider: provider,
			Evidence:      s.Evidence,
		})
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- o.Run(runCtx) }()

	wait := func(want orchestrator.Action) error {
		select {
		case r := <-results:
			if r.err != nil {
				return fmt.Errorf("demo: %s: %w", r.action, r.err)
			}
			if r.action != want {
				return fmt.Errorf("demo: got action %s, want %s", r.action, want)
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	send := func(sig orchestrator.Signal) {
		sig.Repo = repoName
		if sig.Evidence == nil {
			sig.Evidence = map[string]any{}
		}
		if sig.At.IsZero() {
			sig.At = time.Now().UTC()
		}
		o.Signals <- sig
	}

	runCIRed := func() error {
		_, _ = fmt.Fprintln(out, "--- scenario: ci-red ---")
		send(orchestrator.Signal{
			Source:   orchestrator.SourceCI,
			Kind:     orchestrator.KindFail,
			Evidence: map[string]any{"conclusion": "failure", "demo": "ci-red"},
		})
		return wait(orchestrator.ActionFix)
	}

	runSmokeRed := func() error {
		_, _ = fmt.Fprintln(out, "--- scenario: smoke-red ---")
		// map smoke-red → DevGate pass → Promote (issue #5)
		res, err := smoke.Check(ctx, repoName)
		if err != nil {
			return fmt.Errorf("demo: smoke check: %w", err)
		}
		if res.Status != gate.StatusPass {
			return fmt.Errorf("demo: smoke expected pass, got %s", res.Status)
		}
		sha, err := mgr.RemoteSHA(ctx, repoName)
		if err != nil {
			return fmt.Errorf("demo: gated sha: %w", err)
		}
		send(orchestrator.Signal{
			Source:   orchestrator.SourceDevGate,
			Kind:     orchestrator.KindPass,
			SHA:      sha,
			Evidence: res.Evidence,
		})
		return wait(orchestrator.ActionPromote)
	}

	runProdBreach := func() error {
		_, _ = fmt.Fprintln(out, "--- scenario: prod-breach ---")
		res, err := prod.Check(ctx, repoName)
		if err != nil {
			return fmt.Errorf("demo: prod-health check: %w", err)
		}
		kind := orchestrator.KindPass
		if res.Status == gate.StatusFail {
			kind = orchestrator.KindBreach
		}
		if kind != orchestrator.KindBreach {
			return fmt.Errorf("demo: prod-health expected breach, got pass")
		}
		send(orchestrator.Signal{
			Source:   orchestrator.SourceProdHealth,
			Kind:     kind,
			Evidence: res.Evidence,
		})
		return wait(orchestrator.ActionRevert)
	}

	var runErr error
	switch scenario {
	case "ci-red":
		runErr = runCIRed()
	case "smoke-red":
		runErr = runSmokeRed()
	case "prod-breach":
		runErr = runProdBreach()
	case "all":
		if runErr = runCIRed(); runErr != nil {
			break
		}
		if runErr = runSmokeRed(); runErr != nil {
			break
		}
		runErr = runProdBreach()
	default:
		runErr = fmt.Errorf("demo: unknown scenario %q (want ci-red|smoke-red|prod-breach|all)", scenario)
	}

	cancel()
	<-errCh
	if runErr != nil {
		return runErr
	}
	if metas, lerr := sessions.List("", 1); lerr == nil && len(metas) > 0 {
		_, _ = fmt.Fprintf(out, "fix session: xdlc sessions show %s --diff --dir %s\n", metas[0].ID, sessionDir)
	}
	_, _ = fmt.Fprintln(out, "demo: ok")
	return nil
}

func newRunner(provider string) (subagent.Runner, error) {
	if provider == "fake" {
		return &fakeRunner{}, nil
	}
	p := subagent.Provider(provider)
	if !subagent.KnownProvider(provider) {
		var names []string
		for _, known := range subagent.Providers() {
			names = append(names, string(known))
		}
		return nil, fmt.Errorf("demo: unknown provider %q (want %s|fake)", provider, strings.Join(names, "|"))
	}
	r := subagent.NewSubprocessRunner(p, "", nil, 10*time.Minute, nil)
	if _, err := exec.LookPath(r.Binary); err != nil {
		return nil, fmt.Errorf("demo: provider %s binary %q not on PATH; use --provider fake", provider, r.Binary)
	}
	return r, nil
}

// setupOrigin builds bare origin + work clone: main@working, develop@broken
// (one commit ahead) — same fast-forward geometry as dispatch_test.setupOriginBranches.
func setupOrigin(root string) (bareDir, workDir string, err error) {
	bareDir = filepath.Join(root, "origin.git")
	seedDir := filepath.Join(root, "seed")
	workDir = filepath.Join(root, "repo")

	if err := git(root, "init", "--bare", bareDir); err != nil {
		return "", "", err
	}
	if err := git(root, "clone", bareDir, seedDir); err != nil {
		return "", "", err
	}
	if err := git(seedDir, "config", "user.email", "demo@xdlc.local"); err != nil {
		return "", "", err
	}
	if err := git(seedDir, "config", "user.name", "xdlc-demo"); err != nil {
		return "", "", err
	}

	if err := git(seedDir, "checkout", "-b", "main"); err != nil {
		return "", "", err
	}
	if err := writeTree(seedDir, true); err != nil {
		return "", "", err
	}
	if err := git(seedDir, "add", "."); err != nil {
		return "", "", err
	}
	if err := git(seedDir, "commit", "-m", "init on main"); err != nil {
		return "", "", err
	}
	if err := git(seedDir, "push", "origin", "main"); err != nil {
		return "", "", err
	}

	if err := git(seedDir, "checkout", "-b", "develop"); err != nil {
		return "", "", err
	}
	// ponytail: intentional bug so CI-red / Fix has something to do
	broken := "package demo\n\nfunc Add(a, b int) int { return a - b }\n"
	if err := os.WriteFile(filepath.Join(seedDir, "add.go"), []byte(broken), 0o644); err != nil { //nolint:gosec // G306: fixture in throwaway demo tree
		return "", "", err
	}
	if err := git(seedDir, "add", "."); err != nil {
		return "", "", err
	}
	if err := git(seedDir, "commit", "-m", "break Add on develop"); err != nil {
		return "", "", err
	}
	if err := git(seedDir, "push", "origin", "develop"); err != nil {
		return "", "", err
	}
	if err := git(bareDir, "symbolic-ref", "HEAD", "refs/heads/develop"); err != nil {
		return "", "", err
	}

	if err := git(root, "clone", "--branch", "develop", bareDir, workDir); err != nil {
		return "", "", err
	}
	if err := git(workDir, "config", "user.email", "demo@xdlc.local"); err != nil {
		return "", "", err
	}
	if err := git(workDir, "config", "user.name", "xdlc-demo"); err != nil {
		return "", "", err
	}
	return bareDir, workDir, nil
}

func writeTree(dir string, working bool) error {
	body := "package demo\n\nfunc Add(a, b int) int { return a - b }\n"
	if working {
		body = "package demo\n\nfunc Add(a, b int) int { return a + b }\n"
	}
	files := map[string]string{
		"go.mod": "module example.com/xdlc-demo\n\ngo 1.21\n",
		"add.go": body,
		"add_test.go": `package demo

import "testing"

func TestAdd(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Fatalf("Add(2,3)=%d want 5", Add(2, 3))
	}
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil { //nolint:gosec // G306: fixture in throwaway demo tree
			return err
		}
	}
	return nil
}

func git(dir string, args ...string) error {
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...) //nolint:gosec // G204: fixed git verb + local demo paths
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %v: %w: %s", args, err, strings.TrimSpace(string(out)))
	}
	return nil
}
