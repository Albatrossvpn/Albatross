//go:build linux

package core

import (
	"os"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// On Linux, Android included, cores are started, stopped and waited for
// with the plain system calls, not with os/exec. Before its first process
// Go's os package probes the pidfd calls (Go issue 69065), and Android 8 to
// 11 forbid some of them to apps: the forbidden call kills the whole app.

// child is a running core.
type child struct {
	id   int
	mu   sync.Mutex
	gone bool // reaped: its number may already be another process's
}

func spawn(bin string, args, env []string, dir string, out *os.File) (*child, error) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		return nil, err
	}
	defer null.Close()
	pid, err := syscall.ForkExec(bin, append([]string{bin}, args...), &syscall.ProcAttr{
		Dir:   dir,
		Env:   env,
		Files: []uintptr{null.Fd(), out.Fd(), out.Fd()},
		// The core dies with this app.
		Sys: &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL},
	})
	if err != nil {
		return nil, &os.PathError{Op: "fork/exec", Path: bin, Err: err}
	}
	return &child{id: pid}, nil
}

func (c *child) pid() int { return c.id }

// wait returns once the core has exited, and reaps it.
func (c *child) wait() {
	// Wait without reaping first, so that no signal can reach another
	// process given the same number.
	var info unix.Siginfo
	for unix.Waitid(unix.P_PID, c.id, &info, unix.WEXITED|unix.WNOWAIT, nil) == unix.EINTR {
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gone = true
	var ws syscall.WaitStatus
	for {
		if _, err := syscall.Wait4(c.id, &ws, 0, nil); err != syscall.EINTR {
			return
		}
	}
}

func (c *child) signal(sig syscall.Signal) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.gone {
		syscall.Kill(c.id, sig)
	}
}

func (c *child) stop() { c.signal(syscall.SIGTERM) }
func (c *child) kill() { c.signal(syscall.SIGKILL) }
