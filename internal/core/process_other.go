//go:build !windows

package core

import (
	"os"
	"os/exec"
	"syscall"
)

func hideWindow(cmd *exec.Cmd) {
	setPdeathsig(cmd)
}

func attachToJob(*os.Process) {}

func stopProcess(p *os.Process) { p.Signal(syscall.SIGTERM) }
