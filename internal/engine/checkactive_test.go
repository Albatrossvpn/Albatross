//go:build unix

package engine

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
	"time"

	"github.com/Albatrossvpn/vp/internal/core"
	"github.com/Albatrossvpn/vp/internal/sources"
)

// A server that answers its own check but through which no name resolves
// (TUN mode: the device's lookups go through it) is left for a backup at
// the second failed lookup, as one that stopped answering would be.
func TestCheckActiveLeavesAServerWhoseNamesFail(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep to stand in for the core")
	}
	f := &fakeCore{delayOK: true, dnsCode: http.StatusInternalServerError}
	srv := httptest.NewServer(f)
	defer srv.Close()
	p, err := core.Start("sing-box", sleep, []string{"60"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	e, err := New(Options{DataDir: t.TempDir(), Sources: []sources.Source{}})
	if err != nil {
		t.Fatal(err)
	}
	defer e.pool.Close()
	m0, m1 := fakeMember("m0"), fakeMember("m1")
	c := &connection{
		mode: "tun", home: "DE", clashAddr: srv.Listener.Addr().String(), clashSecret: "s",
		running: &core.Running{SingBox: p}, wake: make(chan struct{}, 1),
		members: []*member{m0, m1}, active: m0, switched: time.Now().Add(-time.Hour),
	}
	ctx := t.Context()
	if !e.checkActive(ctx, c, false) {
		t.Fatal("the connection was replaced")
	}
	f.mu.Lock()
	sel := f.selected
	f.mu.Unlock()
	if sel != "" || c.activeMember() != m0 {
		t.Fatalf("left the server at the first failed lookup (now %q)", sel)
	}
	if !e.checkActive(ctx, c, false) {
		t.Fatal("the connection was replaced instead of switched")
	}
	f.mu.Lock()
	sel = f.selected
	f.mu.Unlock()
	if sel != "m1" || c.activeMember() != m1 {
		t.Fatalf("still on the server no name resolves through (now %q)", sel)
	}
	// Names fail through the backup too, and back it goes; a third time in
	// a few minutes, the resolver is what is out of reach, and it stays.
	for range 2 {
		e.checkActive(ctx, c, false)
	}
	if c.activeMember() != m0 {
		t.Fatal("did not leave the second server whose names fail")
	}
	for range 4 {
		e.checkActive(ctx, c, false)
	}
	if c.activeMember() != m0 {
		t.Fatal("kept switching while names fail through every server")
	}
}
