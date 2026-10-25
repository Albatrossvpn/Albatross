//go:build linux

package core

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// A core's output is read, it stops when told to, and one that quits is
// seen to.
func TestStartStop(t *testing.T) {
	p, err := Start("sh", "/bin/sh", []string{"-c", "echo ready; exec sleep 30"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100 && !strings.Contains(p.Output(), "ready"); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if p.Output() != "ready" || p.Pid() <= 0 || p.Exited() {
		t.Fatalf("output %q, pid %d, exited %v", p.Output(), p.Pid(), p.Exited())
	}
	start := time.Now()
	p.Stop()
	if !p.Exited() || time.Since(start) > 2*time.Second {
		t.Fatalf("stopped after %v: %v", time.Since(start), p.Exited())
	}
	p.Stop() // stopping twice is harmless

	q, err := Start("sh", "/bin/sh", []string{"-c", "exit 3"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-q.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("an exited core was not seen to exit")
	}
	if _, err := Start("none", "/nonexistent/core", nil, nil, nil); err == nil {
		t.Fatal("a missing core started")
	}
}

// TestStartWithoutPidfd forbids the pidfd calls the way Android 8 to 11 do
// (a seccomp filter that traps them) and checks that cores still start and
// stop: with os/exec the first start kills the process.
func TestStartWithoutPidfd(t *testing.T) {
	if mode := os.Getenv("ALBATROSS_PIDFD_CHILD"); mode != "" {
		forbidPidfd()
		if mode == "exec" {
			fmt.Println(exec.Command("/bin/true").Run())
			os.Exit(0)
		}
		p, err := Start("sh", "/bin/sh", []string{"-c", "echo started; exec sleep 30"}, nil, nil)
		if err != nil {
			fmt.Println("start:", err)
			os.Exit(3)
		}
		for i := 0; i < 100 && p.Output() == ""; i++ {
			time.Sleep(20 * time.Millisecond)
		}
		p.Stop()
		fmt.Println(p.Output(), "and stopped:", p.Exited())
		os.Exit(0)
	}
	run := func(mode string) (string, error) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestStartWithoutPidfd$", "-test.v")
		cmd.Env = append(os.Environ(), "ALBATROSS_PIDFD_CHILD="+mode)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run("exec"); err == nil || !strings.Contains(out, "SIGSYS") {
		t.Fatalf("the filter did not stop os/exec (%v):\n%s", err, out)
	}
	if out, err := run("core"); err != nil || !strings.Contains(out, "started and stopped: true") {
		t.Fatalf("the core did not start and stop (%v):\n%s", err, out)
	}
}

// forbidPidfd traps the pidfd calls and clone3 in every thread.
func forbidPidfd() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	filter := []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0}, // seccomp_data.nr
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 3, K: unix.SYS_PIDFD_OPEN},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 2, K: unix.SYS_PIDFD_SEND_SIGNAL},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 1, K: unix.SYS_CLONE3},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_TRAP},
	}
	prog := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		fmt.Println("no_new_privs:", err)
		os.Exit(3)
	}
	if _, _, e := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER, unix.SECCOMP_FILTER_FLAG_TSYNC, uintptr(unsafe.Pointer(&prog))); e != 0 {
		fmt.Println("seccomp:", e)
		os.Exit(3)
	}
}
