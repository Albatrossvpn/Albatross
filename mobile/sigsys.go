//go:build linux && cgo

package mobile

// Android 8 to 11 forbid apps some newer system calls (seccomp), and a
// forbidden call raises SIGSYS, which kills the process. Go probes the
// pidfd calls before it starts its first process and counts on its own
// SIGSYS handler to turn that into an error (Go issue 69065), but in a
// library such as this one Go leaves SIGSYS to whoever loads it: the probe
// killed the app as soon as it started a core. Cores no longer start
// through os/exec (internal/core/child_linux.go); in case other code does,
// the handler below, set when the library loads, makes every call the
// filter forbids fail with ENOSYS, as on a kernel that lacks it. Any other
// SIGSYS goes to the handler that was there before (the system's crash
// reporter).

/*
#define _GNU_SOURCE
#include <errno.h>
#include <signal.h>
#include <string.h>
#include <ucontext.h>

#ifndef SYS_SECCOMP
#define SYS_SECCOMP 1
#endif

#if defined(__aarch64__) || defined(__arm__) || defined(__x86_64__) || defined(__i386__)
#define ALBATROSS_SIGSYS 1
#endif

static struct sigaction albatross_prev_sigsys;

static void albatross_sigsys(int sig, siginfo_t *info, void *ctx) {
	if (info != NULL && info->si_code == SYS_SECCOMP && ctx != NULL) {
		// The call did not run; its result register is what the caller sees.
		ucontext_t *uc = (ucontext_t *)ctx;
#if defined(__aarch64__)
		uc->uc_mcontext.regs[0] = (unsigned long long)-ENOSYS;
#elif defined(__arm__)
		uc->uc_mcontext.arm_r0 = (unsigned long)-ENOSYS;
#elif defined(__x86_64__)
		uc->uc_mcontext.gregs[REG_RAX] = -ENOSYS;
#elif defined(__i386__)
		uc->uc_mcontext.gregs[REG_EAX] = -ENOSYS;
#endif
		return;
	}
	struct sigaction *p = &albatross_prev_sigsys;
	if (p->sa_flags & SA_SIGINFO) {
		if (p->sa_sigaction != NULL) {
			p->sa_sigaction(sig, info, ctx);
		}
	} else if (p->sa_handler == SIG_DFL) {
		signal(sig, SIG_DFL);
		raise(sig);
	} else if (p->sa_handler != SIG_IGN) {
		p->sa_handler(sig);
	}
}

static void albatross_catch_sigsys(void) {
#ifdef ALBATROSS_SIGSYS
	struct sigaction sa;
	memset(&sa, 0, sizeof sa);
	sa.sa_sigaction = albatross_sigsys;
	// Go's threads have small stacks; its signal stack is the one to use.
	sa.sa_flags = SA_SIGINFO | SA_ONSTACK;
	sigemptyset(&sa.sa_mask);
	sigaction(SIGSYS, &sa, &albatross_prev_sigsys);
#endif
}

#ifdef __ANDROID__
__attribute__((constructor)) static void albatross_catch_sigsys_at_load(void) {
	albatross_catch_sigsys();
}
#endif
*/
import "C"

// catchSIGSYS sets the handler (the library does at load on Android).
func catchSIGSYS() { C.albatross_catch_sigsys() }
