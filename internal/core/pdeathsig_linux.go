//go:build linux

package core

import (
	"os/exec"
	"syscall"
)

// setPdeathsig kills the core if this app dies.
func setPdeathsig(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
