package tester

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/Albatrossvpn/vp/internal/node"
)

// An OpenVPN outbound refuses connections until its session stands: a
// failure that comes at once is tried again until the session answers; a
// slow failure is the answer, and other servers get one try.
func TestProbeSessionWaitsForTheSession(t *testing.T) {
	old := sessionPause
	sessionPause = 20 * time.Millisecond
	defer func() { sessionPause = old }()
	calls := 0
	notYet := func(context.Context, *url.URL, time.Duration) Result {
		calls++
		if calls < 4 {
			return Result{Err: "connection closed"}
		}
		return Result{OK: true, Delay: time.Millisecond}
	}
	if r := probeSession(context.Background(), nil, 5*time.Second, notYet); !r.OK || calls != 4 {
		t.Errorf("session not waited for: ok=%v after %d tries", r.OK, calls)
	}
	calls = 0
	slow := func(context.Context, *url.URL, time.Duration) Result {
		calls++
		time.Sleep(3 * sessionPause)
		return Result{Err: "timeout"}
	}
	if r := probeSession(context.Background(), nil, 5*time.Second, slow); r.OK || calls != 1 {
		t.Errorf("a slow failure was tried %d times", calls)
	}
	calls = 0
	never := func(context.Context, *url.URL, time.Duration) Result { calls++; return Result{Err: "refused"} }
	if r := probeSession(context.Background(), nil, 300*time.Millisecond, never); r.OK || calls > 20 {
		t.Errorf("past its limit: %d tries", calls)
	}
	if !sessionFirst(&node.Node{Proto: "openvpn"}) || sessionFirst(&node.Node{Proto: "vless"}) {
		t.Error("only OpenVPN waits for its session")
	}
}
