//go:build !linux

package core

import (
	"os"
	"os/exec"
)

// child is a running core.
type child struct{ cmd *exec.Cmd }

func spawn(bin string, args, env []string, dir string, out *os.File) (*child, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, env, out, out
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	attachToJob(cmd.Process)
	return &child{cmd}, nil
}

func (c *child) pid() int { return c.cmd.Process.Pid }
func (c *child) wait()    { c.cmd.Wait() }
func (c *child) stop()    { stopProcess(c.cmd.Process) }
func (c *child) kill()    { c.cmd.Process.Kill() }
