// Package core starts and stops the proxy cores (sing-box and Xray) and
// turns sets of nodes into configs those cores accept.
package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Binaries are the core executables.
type Binaries struct {
	SingBox string
	Xray    string
}

// Locate finds the cores next to the app or in its data directory.
func Locate(dirs ...string) Binaries {
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	find := func(name string) string {
		for _, d := range dirs {
			for _, p := range []string{filepath.Join(d, "core", name+exe), filepath.Join(d, name+exe)} {
				if st, err := os.Stat(p); err == nil && !st.IsDir() {
					return p
				}
			}
		}
		if p, err := exec.LookPath(name + exe); err == nil {
			return p
		}
		return ""
	}
	return Binaries{SingBox: find("sing-box"), Xray: find("xray")}
}

// Process is a running core.
type Process struct {
	Name string
	cmd  *exec.Cmd
	done chan struct{}

	mu   sync.Mutex
	logs []string
	err  error
}

const keepLogLines = 300

// Start launches a core with its output captured.
func Start(name, bin string, args []string, env []string, onLine func(string)) (*Process, error) {
	if bin == "" {
		return nil, fmt.Errorf("%s is not installed", name)
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = filepath.Dir(bin)
	cmd.Env = append(os.Environ(), env...)
	hideWindow(cmd)
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		return nil, err
	}
	attachToJob(cmd.Process)
	p := &Process{Name: name, cmd: cmd, done: make(chan struct{})}
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			line := stripANSI(sc.Text())
			p.mu.Lock()
			p.logs = append(p.logs, line)
			if len(p.logs) > keepLogLines {
				p.logs = p.logs[len(p.logs)-keepLogLines:]
			}
			p.mu.Unlock()
			if onLine != nil {
				onLine(line)
			}
		}
	}()
	go func() {
		err := cmd.Wait()
		pw.Close()
		p.mu.Lock()
		p.err = err
		p.mu.Unlock()
		close(p.done)
	}()
	return p, nil
}

// Done is closed when the process exits.
func (p *Process) Done() <-chan struct{} { return p.done }

// Exited reports whether the process has stopped.
func (p *Process) Exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// Logs returns the recent output.
func (p *Process) Logs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.logs...)
}

// Output returns the recent output as one string.
func (p *Process) Output() string { return strings.Join(p.Logs(), "\n") }

// Stop kills the process and waits for it.
func (p *Process) Stop() {
	if p == nil || p.Exited() {
		return
	}
	stopProcess(p.cmd.Process)
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		p.cmd.Process.Kill()
		<-p.done
	}
}

// WaitListening waits until addr accepts TCP connections, the process
// exits, or the timeout passes.
func (p *Process) WaitListening(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if p.Exited() {
			// Give the log reader a moment to drain the final lines.
			time.Sleep(50 * time.Millisecond)
			return &ExitError{Output: p.Output()}
		}
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not start listening on %s", p.Name, addr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(60 * time.Millisecond):
		}
	}
}

// ExitError carries the output of a core that quit while starting.
type ExitError struct{ Output string }

func (e *ExitError) Error() string {
	lines := strings.Split(strings.TrimSpace(e.Output), "\n")
	return "core exited: " + lines[len(lines)-1]
}

// IsExit reports whether err is a startup exit.
func IsExit(err error) (*ExitError, bool) {
	var e *ExitError
	ok := errors.As(err, &e)
	return e, ok
}

func stripANSI(s string) string {
	if !strings.Contains(s, "\x1b[") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < '@' || s[j] > '~') {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// FreePorts reserves n distinct free TCP ports on 127.0.0.1.
func FreePorts(n int) ([]int, error) {
	var ls []net.Listener
	defer func() {
		for _, l := range ls {
			l.Close()
		}
	}()
	ports := make([]int, 0, n)
	for i := 0; i < n; i++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		ls = append(ls, l)
		ports = append(ports, l.Addr().(*net.TCPAddr).Port)
	}
	return ports, nil
}
