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
	probe.set(cur.tag, 100<<10)
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
