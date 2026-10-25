//go:build !windows && !linux

package core

import (
	"os"
	"os/exec"
	"syscall"
)

func hideWindow(*exec.Cmd) {}

func attachToJob(*os.Process) {}

func stopProcess(p *os.Process) { p.Signal(syscall.SIGTERM) }
