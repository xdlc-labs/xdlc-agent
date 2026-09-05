//go:build unix

package gate

import (
	"os/exec"
	"syscall"
	"time"
)

// configureKillGroup makes Cancel kill the whole process group so a
// timed-out external gate does not leave children behind (issue #11).
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
