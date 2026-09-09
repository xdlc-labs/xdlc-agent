//go:build unix

package subagent

import (
	"os/exec"
	"syscall"
	"time"
)

// configureKillGroup makes Cancel kill the whole process group so
// timeout does not leave node/git children behind (issue #11).
func configureKillGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 3 * time.Second
}

// killProcessGroup kills the agent and everything it spawned. The
// watchdog needs its own kill path: the context is not what expired, so
// cmd.Cancel is never called, and killing only the direct child would
// leave a wedged node or git process holding the worktree.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
