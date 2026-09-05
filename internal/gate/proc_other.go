//go:build !unix

package gate

import "os/exec"

func configureKillGroup(cmd *exec.Cmd) {}
