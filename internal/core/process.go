// Package core starts and stops the proxy cores (sing-box and Xray) and
// turns sets of nodes into configs those cores accept.
package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
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
	SingBox  string
	Xray     string
	WarpPlus string
	Usque    string // WARP over MASQUE
	Tor      string
	// Snowflake is the Snowflake proxy for "Help others" (tools/snowflake).
	Snowflake string
}

// Locate finds the cores next to the app or in its data directory.
func Locate(dirs ...string) Binaries {
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	find := func(name string) string {
		// Android only runs executables shipped as native libraries
		// (lib<name>.so in the app's library directory).
		lib := "lib" + strings.ReplaceAll(name, "-", "") + ".so"
		for _, d := range dirs {
			for _, p := range []string{filepath.Join(d, "core", name+exe), filepath.Join(d, name+exe), filepath.Join(d, lib)} {
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
	tor := ""
	// The Tor Expert Bundle keeps tor and its transports in a folder of
	// their own.
	for _, d := range dirs {
		if p := filepath.Join(d, "core", "tor", "tor"+exe); fileExists(p) {
			tor = p
			break
		}
	}
	if tor == "" {
		tor = find("tor")
	}
	return Binaries{SingBox: find("sing-box"), Xray: find("xray"), WarpPlus: find("warp-plus"), Usque: find("usque"), Tor: tor, Snowflake: find("snowflake-helper")}
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// Process is a running core.
type Process struct {
	Name  string
	child *child
	done  chan struct{}

	mu   sync.Mutex
	logs []string
}

const keepLogLines = 300

// Start launches a core with its output captured.
func Start(name, bin string, args []string, env []string, onLine func(string)) (*Process, error) {
	if bin == "" {
		return nil, fmt.Errorf("%s is not installed", name)
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	c, err := spawn(bin, args, mergeEnv(os.Environ(), env), filepath.Dir(bin), pw)
	pw.Close()
	if err != nil {
		pr.Close()
		return nil, err
	}
	p := &Process{Name: name, child: c, done: make(chan struct{})}
	go func() {
		defer pr.Close()
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
		c.wait()
		close(p.done)
	}()
	return p, nil
}

// Pid is the operating-system process ID.
func (p *Process) Pid() int { return p.child.pid() }

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
	p.child.stop()
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		p.child.kill()
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
			err := fmt.Errorf("%s did not start listening on %s", p.Name, addr)
			if out := strings.TrimSpace(p.Output()); out != "" {
				lines := strings.Split(out, "\n")
				if len(lines) > 5 {
					lines = lines[len(lines)-5:]
				}
				err = fmt.Errorf("%w: %s", err, strings.Join(lines, " | "))
			}
			return err
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

// mergeEnv returns base with the variables in extra set, one entry per
// name. The C library takes the first entry of a repeated name, so a plain
// append would leave the parent's value in charge.
func mergeEnv(base, extra []string) []string {
	out := make([]string, 0, len(base)+len(extra))
	seen := map[string]int{}
	for _, kv := range append(append([]string{}, base...), extra...) {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if j, ok := seen[name]; ok {
			out[j] = kv
			continue
		}
		seen[name] = len(out)
		out = append(out, kv)
	}
	return out
}
