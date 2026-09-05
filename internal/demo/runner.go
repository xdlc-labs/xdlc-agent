package demo

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// fakeRunner "fixes" the broken Add by committing the correct body and
// pushing develop — same shape as dispatch_test's fakeRunner.
type fakeRunner struct{}

func (f *fakeRunner) Run(ctx context.Context, dir, _ string, _ []string) (string, error) {
	path := filepath.Join(dir, "add.go")
	const fixed = "package demo\n\nfunc Add(a, b int) int { return a + b }\n"
	if err := os.WriteFile(path, []byte(fixed), 0o644); err != nil {
		return "", err
	}
	if out, err := exec.CommandContext(ctx, "git", "-C", dir, "add", "add.go").CombinedOutput(); err != nil {
		return string(out), fmt.Errorf("fake runner: git add: %w: %s", err, out)
	}
	if out, err := exec.CommandContext(ctx, "git", "-C", dir, "commit", "-m", "fix: Add returns a+b").CombinedOutput(); err != nil {
		return string(out), fmt.Errorf("fake runner: git commit: %w: %s", err, out)
	}
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "push", "origin", "develop").CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("fake runner: git push: %w: %s", err, out)
	}
	return string(out), nil
}
