package engine

import (
	"testing"
	"time"
)

func poked(c *connection) bool {
	select {
	case <-c.wake:
		return true
	default:
		return false
	}
}

// One site failing over and over says nothing about the server in use;
// several sites failing together do.
func TestWatchLogNeedsSeveralSites(t *testing.T) {
	c := &connection{wake: make(chan struct{}, 1)}
	line := func(host string) string {
		return "ERROR [123 1.2s] connection: open connection to " + host + ":443 using outbound/selector[proxy]: connection reset by peer"
	}
	for range 10 {
		c.watchLog("sing-box", line("down.example"))
	}
	c.watchLog("sing-box", "ERROR connection: open connection to x.ir:443 using outbound/direct[direct]: timeout")
	if poked(c) {
		t.Fatal("one failing site triggered a check")
	}
	c.watchLog("sing-box", line("a.example"))
	if poked(c) {
		t.Fatal("two failing sites triggered a check")
	}
	c.watchLog("sing-box", line("b.example"))
	if !poked(c) {
		t.Fatal("three failing sites did not trigger a check")
	}
	// Not reaching the server at all is checked at once.
	c.watchLog("sing-box", "ERROR [9 0ms] connection: open connection to ipinfo.io:443 using outbound/selector[proxy]: dial tcp 10.1.0.173:44387: connect: connection refused")
	if !poked(c) {
		t.Fatal("an unreachable server did not trigger a check")
	}
}

// Sending a lot with little coming back is an upload, not a stall.
func TestUploadIsNotStall(t *testing.T) {
	c := &connection{wake: make(chan struct{}, 1)}
	for range 6 {
		c.addTraffic(2<<20, 0)
		c.waiting = c.waiting.Add(-time.Second)
	}
	if poked(c) {
		t.Fatal("an upload was taken for a stalled server")
	}
	// A small request that never gets an answer is.
	c.addTraffic(600, 0)
	c.waiting = c.waiting.Add(-3 * time.Second)
	c.addTraffic(0, 0)
	if !poked(c) {
		t.Fatal("a stall was missed")
	}
}

// Backups measured fast come first, untested ones next, slow ones last.
func TestBackupsPreferMeasuredSpeed(t *testing.T) {
	now := time.Now()
	m := func(tag string, delay int, speed float64) *member {
		x := &member{tag: tag, country: "DE", alive: true, delay: delay, speed: speed}
		if speed > 0 {
			x.speedAt = now
		}
		return x
	}
	c := &connection{home: "DE", members: []*member{
		m("slow-low-ping", 50, 40<<10), m("untested", 80, 0), m("fast", 300, 5<<20), m("faster", 400, 9<<20),
		{tag: "elsewhere", country: "NL", alive: true, delay: 10},
	}}
	var got []string
	for _, b := range c.backups(nil) {
		got = append(got, b.tag)
	}
	want := []string{"faster", "fast", "untested", "slow-low-ping", "elsewhere"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order %v, want %v", got, want)
		}
	}
}

// Swapping to a member with a lower delay waits until the one in use has
// been in use for a while, and skips members measured slow.
func TestFasterRespectsStayAndSpeed(t *testing.T) {
	cur := &member{tag: "cur", country: "DE", alive: true, delay: 500}
	quick := &member{tag: "quick", country: "DE", alive: true, delay: 100}
	c := &connection{home: "DE", active: cur, members: []*member{cur, quick}, switched: time.Now()}
	if c.faster() != nil {
		t.Fatal("swapped right after a swap")
	}
	c.switched = time.Now().Add(-time.Hour)
	if c.faster() != quick {
		t.Fatal("missed a clearly faster member")
	}
	quick.speed, quick.speedAt = 20<<10, time.Now()
	if c.faster() != nil {
		t.Fatal("swapped to a member measured slow")
	}
}
