//go:build !unix

package subagent

import "os/exec"

// configureKillGroup is a no-op on non-unix: CommandContext still
// cancels the direct child; process-group kill is unix-only.
func configureKillGroup(cmd *exec.Cmd) {}
