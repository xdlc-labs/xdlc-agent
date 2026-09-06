package demo

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/xdlc-labs/xdlc-agent/internal/subagent"
)

// fakeRunner "fixes" the broken Add by committing the correct body and
// pushing develop — same shape as dispatch_test's fakeRunner.
type fakeRunner struct{}

func (f *fakeRunner) Run(ctx context.Context, dir, _ string, _ []string) (string, error) {
	path := filepath.Join(dir, "add.go")
	const fixed = "package demo\n\nfunc Add(a, b int) int { return a + b }\n"
	if err := os.WriteFile(path, []byte(fixed), 0o644); err != nil { //nolint:gosec // G306: demo fixture tree
		return "", err
	}
	if out, err := exec.CommandContext(ctx, "git", "-C", dir, "add", "add.go").CombinedOutput(); err != nil { //nolint:gosec // G204: fixed git args
		return string(out), fmt.Errorf("fake runner: git add: %w: %s", err, out)
	}
	if out, err := exec.CommandContext(ctx, "git", "-C", dir, "commit", "-m", "fix: Add returns a+b").CombinedOutput(); err != nil { //nolint:gosec // G204: fixed git args
		return string(out), fmt.Errorf("fake runner: git commit: %w: %s", err, out)
	}
	// No push: the demo runs with per-Fix worktrees on, like a real
	// install, so the agent's job ends at the commit and xdlc pushes the
	// scratch branch where it belongs.
	//
	// Close with the verdict line a real coding agent is asked for, so
	// `xdlc demo` shows the same OUTCOME / SUMMARY an operator will see
	// in a live Fix instead of the empty compatibility path.
	return fmt.Sprintf(`{"%s": "%s", "summary": "Add returned a-b; corrected it to a+b"}`,
		subagent.VerdictKey, subagent.OutcomeFixed), nil
}
