//go:build !linux && !windows

package core

import "os/exec"

func setPdeathsig(*exec.Cmd) {}
