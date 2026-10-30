package engine

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Albatrossvpn/vp/internal/sources"
)

// fakeProbe stands in for the core's probe inbound: the user names the
// member a speed test goes through, and each member carries at its own
// rate (bytes a second; 0 = unlimited, below 0 = nothing gets through).
type fakeProbe struct {
	mu   sync.Mutex
	rate map[string]int64
}

func (f *fakeProbe) set(tag string, r int64) {
	f.mu.Lock()
	f.rate[tag] = r
	f.mu.Unlock()
}

func (f *fakeProbe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tag := ""
	if a, ok := strings.CutPrefix(r.Header.Get("Proxy-Authorization"), "Basic "); ok {
		if b, err := base64.StdEncoding.DecodeString(a); err == nil {
			tag, _, _ = strings.Cut(string(b), ":")
		}
	}
	f.mu.Lock()
	lim := f.rate[tag]
	f.mu.Unlock()
	if lim < 0 {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("bytes"))
	w.Header().Set("Content-Length", strconv.Itoa(n))
	chunk := make([]byte, 4<<10)
	for n > 0 {
		k := min(n, len(chunk))
		if _, err := w.Write(chunk[:k]); err != nil {
			return
		}
		w.(http.Flusher).Flush()
		n -= k
		if lim > 0 {
			time.Sleep(time.Duration(float64(k) / float64(lim) * float64(time.Second)))
		}
	}
}

// A member that carries next to nothing, or nothing at all, is left for a
// usable backup as soon as it is measured, also within stayAtLeast of a
// switch; a slow but usable one is kept that soon, and nothing is
// measured before settleFor.
func TestSpeedRoundRescuesFromUnusableServer(t *testing.T) {
	oldWait, oldURL := speedWait, speedURL
	speedWait, speedURL = time.Second, "http://speed.test/__down?bytes="
	defer func() { speedWait, speedURL = oldWait, oldURL }()
	api := &fakeCore{delayOK: true}
	apiSrv := httptest.NewServer(api)
	defer apiSrv.Close()
	probe := &fakeProbe{rate: map[string]int64{}}
	probeSrv := httptest.NewServer(probe)
	defer probeSrv.Close()
	e, err := New(Options{DataDir: t.TempDir(), Sources: []sources.Source{}})
	if err != nil {
		t.Fatal(err)
	}
	defer e.pool.Close()
	m0, m1, m2 := fakeMember("m0"), fakeMember("m1"), fakeMember("m2")
	c := &connection{
		home: "DE", clashAddr: apiSrv.Listener.Addr().String(), clashSecret: "s",
		probeAddr: probeSrv.Listener.Addr().String(), wake: make(chan struct{}, 1),
		members: []*member{m0, m1, m2}, active: m0, lastBusy: time.Now(),
	}
	ctx := t.Context()
	in := func(m *member, since time.Duration) {
		c.mu.Lock()
		c.active, c.switched, c.speedAt = m, time.Now().Add(-since), time.Time{}
		for _, x := range c.members {
			x.speed, x.speedAt = 0, time.Time{}
		}
		c.mu.Unlock()
	}
	now := func() *member { return c.activeMember() }

	// Just switched: not even measured.
	probe.set("m0", 8<<10)
	in(m0, time.Second)
	e.speedRound(ctx, c, true)
	if now() != m0 || !m0.speedAt.IsZero() {
		t.Fatal("measured or left a member before it settled")
	}
	// Settled but young, at 8 KB/s: left for a usable backup.
	in(m0, 2*settleFor)
	e.speedRound(ctx, c, true)
	if now() == m0 {
		t.Fatalf("stayed on a member at %s", rate(m0.speed))
	}
	// Young, and nothing comes through it: left too.
	cur := now()
	probe.set(cur.tag, -1)
	in(cur, 2*settleFor)
	e.speedRound(ctx, c, true)
	if now() == cur {
		t.Fatal("stayed on a member nothing comes through")
	}
	// Young and slow but usable: kept, whatever the backups do.
	cur = now()
	for _, m := range c.members {
		probe.set(m.tag, 0)
	}
	probe.set(cur.tag, 150<<10)
	in(cur, 2*settleFor)
	e.speedRound(ctx, c, false)
	if now() != cur {
		t.Fatal("changed the exit for speed within stayAtLeast of a switch")
	}
	// Past stayAtLeast the same member is swapped for a much faster one.
	in(cur, 2*stayAtLeast)
	e.speedRound(ctx, c, false)
	if now() == cur {
		t.Fatalf("kept a slow member past stayAtLeast (%s)", rate(cur.speed))
	}
}

// rescueRig is a connection of three members behind fake cores, the first
// in use, settled and past stayAtLeast unless young.
func rescueRig(t *testing.T) (*Engine, *connection, *fakeProbe) {
	oldWait, oldURL := speedWait, speedURL
	speedWait, speedURL = 2*time.Second, "http://speed.test/__down?bytes="
	t.Cleanup(func() { speedWait, speedURL = oldWait, oldURL })
	apiSrv := httptest.NewServer(&fakeCore{delayOK: true})
	t.Cleanup(apiSrv.Close)
	probe := &fakeProbe{rate: map[string]int64{}}
	probeSrv := httptest.NewServer(probe)
	t.Cleanup(probeSrv.Close)
	e, err := New(Options{DataDir: t.TempDir(), Sources: []sources.Source{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.pool.Close() })
	m0, m1, m2 := fakeMember("m0"), fakeMember("m1"), fakeMember("m2")
	c := &connection{
		home: "DE", country: "DE", clashAddr: apiSrv.Listener.Addr().String(), clashSecret: "s",
		probeAddr: probeSrv.Listener.Addr().String(), wake: make(chan struct{}, 1),
		members: []*member{m0, m1, m2}, active: m0, lastBusy: time.Now(), switched: time.Now().Add(-2 * stayAtLeast),
	}
	return e, c, probe
}

// When every server is slow the one in use is still left if it carries
// next to nothing and a backup carries clearly more: a slow network is no
// reason to stay on a server that carries nothing. This holds with
// automatic switching off too; a server the user picked is never left.
func TestSpeedRoundRescuesWhenEverythingIsSlow(t *testing.T) {
	e, c, probe := rescueRig(t)
	ctx := t.Context()
	probe.set("m0", -1)
	probe.set("m1", 60<<10)
	probe.set("m2", 70<<10)
	e.speedRound(ctx, c, false)
	if c.activeMember().tag == "m0" {
		t.Fatalf("stayed on a server nothing comes through for backups at 60-70 KB/s")
	}
	if s := c.members[0].server; s.SpeedAt.IsZero() || s.Speed != 0 {
		t.Fatalf("the dead server's speed was not kept on it: %v at %v", s.Speed, s.SpeedAt)
	}

	// Automatic switching off: still rescued.
	e.mu.Lock()
	e.settings.AutoSwitch = false
	e.mu.Unlock()
	e2, c2, probe2 := rescueRig(t)
	e2.mu.Lock()
	e2.settings.AutoSwitch = false
	e2.mu.Unlock()
	probe2.set("m0", 4<<10)
	e2.speedRound(ctx, c2, false)
	if c2.activeMember().tag == "m0" {
		t.Fatal("automatic switching off kept a server at 4 KB/s")
	}
	// ...but not moved for speed alone.
	e3, c3, probe3 := rescueRig(t)
	e3.mu.Lock()
	e3.settings.AutoSwitch = false
	e3.mu.Unlock()
	probe3.set("m0", 200<<10)
	e3.speedRound(ctx, c3, false)
	if c3.activeMember().tag != "m0" {
		t.Fatal("automatic switching off moved off a usable server")
	}

	// Picked by the user: measured, never left.
	e4, c4, probe4 := rescueRig(t)
	c4.pinned = true
	probe4.set("m0", -1)
	e4.speedRound(ctx, c4, false)
	if c4.activeMember().tag != "m0" {
		t.Fatal("left a server the user picked")
	}
}

// A measured speed outlives the group: a server that carried next to
// nothing ranks behind slower-answering ones that were not measured, and a
// fast one ahead of them; one unlucky result is averaged, not taken whole.
func TestSpeedRank(t *testing.T) {
	now := time.Now()
	plain := &Server{Delay: 300, LastOK: now}
	dead := &Server{Delay: 100, LastOK: now}
	dead.noteSpeed(0, now)
	fast := &Server{Delay: 600, LastOK: now}
	fast.noteSpeed(2<<20, now)
	if !(fast.rank(now) < plain.rank(now) && plain.rank(now) < dead.rank(now)) {
		t.Fatalf("rank: fast %.0f, unmeasured %.0f, dead %.0f", fast.rank(now), plain.rank(now), dead.rank(now))
	}
	old := &Server{Delay: 100, LastOK: now}
	old.noteSpeed(0, now.Add(-speedKept-time.Minute))
	if old.speedRank(now) != 0 {
		t.Fatal("a speed measured long ago still counts")
	}
	fast.noteSpeed(0, now)
	if fast.Speed != 1<<20 {
		t.Fatalf("one bad result replaced a good one: %.0f", fast.Speed)
	}
}

// A new connection is not left on a member that answers its test and then
// carries next to nothing: the gate measures it and starts on a working
// member instead, and leaves a member that carries well alone.
func TestGateSpeedStartsOnAServerThatCarries(t *testing.T) {
	old := gateWait
	gateWait = 2 * time.Second
	defer func() { gateWait = old }()
	e, c, probe := rescueRig(t)
	ctx := t.Context()
	probe.set("m0", 8<<10)
	probe.set("m1", -1)
	probe.set("m2", 0)
	if !e.gateSpeed(ctx, c) || c.activeMember().tag != "m2" {
		t.Fatalf("started on %s (m0 %s, m2 %s)", c.activeMember().tag, rate(c.members[0].speed), rate(c.members[2].speed))
	}
	if c.speedAt.IsZero() {
		t.Fatal("the first speed round does not know the connection was just measured")
	}
	e2, c2, _ := rescueRig(t)
	if e2.gateSpeed(ctx, c2) || c2.activeMember().tag != "m0" {
		t.Fatal("moved off a member that carries well")
	}
}

// A trickle that never stalls for a whole second is still caught: requests
// in most seconds of a window that brings back almost nothing.
func TestStarvedTrickle(t *testing.T) {
	c := &connection{wake: make(chan struct{}, 1)}
	now := time.Now()
	fired := 0
	for i := range 45 {
		c.mu.Lock()
		if c.starved(now.Add(time.Duration(i)*time.Second), 2<<10, 3<<10) {
			fired++
		}
		c.mu.Unlock()
	}
	if fired != 1 {
		t.Fatalf("a 3 KB/s trickle fired %d times in 45 s (once, then not before starveBetween)", fired)
	}
	c = &connection{wake: make(chan struct{}, 1)}
	for i := range 45 {
		c.mu.Lock()
		if c.starved(now.Add(time.Duration(i)*time.Second), 2<<10, 200<<10) {
			t.Fatal("a server carrying 200 KB/s was called starved")
		}
		c.mu.Unlock()
	}
}
