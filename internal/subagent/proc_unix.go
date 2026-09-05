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
