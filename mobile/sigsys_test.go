//go:build linux && cgo

package mobile

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// TestForbiddenCallFails forbids pidfd_open the way Android 8 to 11 do (a
// seccomp filter that traps it) and checks that the call then fails with
// ENOSYS instead of killing the process. It runs in a child process, since
// a filter cannot be removed.
func TestForbiddenCallFails(t *testing.T) {
	if os.Getenv("ALBATROSS_SECCOMP_CHILD") != "" {
		forbiddenCall(os.Getenv("ALBATROSS_SECCOMP_CHILD") == "catch")
		return
	}
	run := func(mode string) (string, error) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestForbiddenCallFails$", "-test.v")
		cmd.Env = append(os.Environ(), "ALBATROSS_SECCOMP_CHILD="+mode)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	// Without the handler the call kills the process, as on the phones.
	if out, err := run("plain"); err == nil || !strings.Contains(out, "SIGSYS") {
		t.Fatalf("the filter did not trap the call (%v):\n%s", err, out)
	}
	out, err := run("catch")
	if err != nil || !strings.Contains(out, "pidfd_open: "+unix.ENOSYS.Error()) {
		t.Fatalf("want ENOSYS from the forbidden call (%v):\n%s", err, out)
	}
}

func forbiddenCall(catch bool) {
	// A filter holds for the thread that sets it.
	runtime.LockOSThread()
	if catch {
		catchSIGSYS()
	}
	filter := []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0}, // seccomp_data.nr
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 0, Jf: 1, K: unix.SYS_PIDFD_OPEN},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_TRAP},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
	}
	prog := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		fmt.Println("no_new_privs:", err)
		os.Exit(3)
	}
	if err := unix.Prctl(unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER, uintptr(unsafe.Pointer(&prog)), 0, 0); err != nil {
		fmt.Println("seccomp:", err)
		os.Exit(3)
	}
	_, err := unix.PidfdOpen(os.Getpid(), 0)
	fmt.Println("pidfd_open:", err)
	os.Exit(0)
}
