//go:build !unix

package subagent

import "os/exec"

// configureKillGroup is a no-op on non-unix: CommandContext still
// cancels the direct child; process-group kill is unix-only.
func configureKillGroup(cmd *exec.Cmd) {}

// killProcessGroup falls back to killing the direct child; process-group
// kill is unix-only, so a stalled agent's children may outlive it here.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
