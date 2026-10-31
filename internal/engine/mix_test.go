package engine

import (
	"testing"
	"time"

	"github.com/Albatrossvpn/vp/internal/node"
	"github.com/Albatrossvpn/vp/internal/sources"
)

// An automatic connection is a mix: when the fast servers in the group are
// all plain (throttleable) protocols, the engine keeps one throttle-
// resistant anchor — here a Cloudflare WARP method — in the group even
// though it is slower, so a failover has a steady exit to land on when the
// plain servers are being throttled. It gives up the slowest plain server
// for it, never the fastest. A chosen protocol fills the group with that
// protocol alone, so the anchor is not forced in then.
func TestMixedGroupKeepsAnchor(t *testing.T) {
	e, err := New(Options{DataDir: t.TempDir(), Sources: []sources.Source{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.pool.Close() })
	now := time.Now()
	addLink := func(link, name, proto string, delay int) {
		n, err := node.ParseLink(link)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		e.good[node.Key64(n.Key())] = &Server{
			Key: n.Key(), Link: link, Name: name, Proto: proto, Country: "DE",
			Delay: delay, LastOK: now, TestedAt: now,
		}
	}
	// Three fast plain servers of three different kinds, so the engine's own
	// kind diversity does not pull in a fourth kind on its own.
	addLink("vless://b831381d-6324-4d53-ad4f-8cda48b30811@fast0.example:443?security=tls&type=tcp#fast0", "fast0", "vless", 100)
	addLink("trojan://pw123@fast1.example:443?security=tls&type=tcp#fast1", "fast1", "trojan", 110)
	addLink("vless://b831381d-6324-4d53-ad4f-8cda48b30812@fast2.example:8443?security=tls&type=tcp#fast2", "fast2", "vless", 130)
	// A Cloudflare WARP method: slower, but throttle-resistant.
	wp := node.WarpPlus("warp", "")
	e.good[node.Key64(wp.Key())] = &Server{
		Key: wp.Key(), Link: wp.Link(), Name: "warp", Proto: "warpplus", Source: "warp-plus",
		Country: "DE", Delay: 500, LastOK: now, TestedAt: now,
	}

	names := func() map[string]bool {
		out := map[string]bool{}
		for _, s := range e.bestServers("DE", 3, nil) {
			out[s.Name] = true
		}
		return out
	}

	// The anchor is recognised as throttle-resistant, the plain ones are not.
	if s := e.good[node.Key64(wp.Key())]; !s.throttleResistant() {
		t.Fatal("the WARP method is not seen as throttle-resistant")
	}
	if s := e.good[serverKey(t, e, "fast0")]; s.throttleResistant() {
		t.Fatal("a plain TCP server is wrongly seen as throttle-resistant")
	}

	// Automatic: the anchor is kept though it is slowest, in place of the
	// slowest plain server; the fastest server stays.
	got := names()
	if !got["warp"] {
		t.Fatalf("automatic group dropped the throttle-resistant anchor: %v", got)
	}
	if !got["fast0"] {
		t.Fatalf("automatic group dropped the fastest server: %v", got)
	}
	if got["fast2"] {
		t.Fatalf("automatic group kept the slowest plain server over the anchor: %v", got)
	}

	// A chosen protocol: ranked by speed alone within that protocol, so the
	// anchor of another kind is not forced in.
	e.mu.Lock()
	e.settings.Protocol = "vless"
	e.mu.Unlock()
	if got := names(); got["warp"] {
		t.Fatalf("a chosen protocol still forced the anchor in: %v", got)
	}
}

// throttleResistant marks the anchors of a mix: the WARP/Psiphon/MASQUE
// methods, the device's own Cloudflare WARP, and CDN-fronted servers — but
// not Tor (the slow last resort) nor a plain throttleable server.
func TestThrottleResistant(t *testing.T) {
	resistant := func(proto, source, link string) bool {
		s := &Server{Proto: proto, Source: source, Link: link}
		return s.throttleResistant()
	}
	warp := node.WarpPlus("warp", "")
	cases := []struct {
		name string
		got  bool
		want bool
	}{
		{"warp method", resistant("warpplus", "warp-plus", warp.Link()), true},
		{"psiphon method", resistant("warpplus", "warp-plus", node.WarpPlus("psiphon", "DE").Link()), true},
		{"tor method", resistant("warpplus", "tor", node.WarpPlus("tor", "").Link()), false},
		{"device WARP", resistant("wireguard", sources.Warp().Label(), ""), true},
		{"CDN-fronted vless-ws", resistant("vless", "a.example", "vless://b831381d-6324-4d53-ad4f-8cda48b30811@cdn.example:443?security=tls&type=ws&host=cdn.example&path=%2F#c"), true},
		{"plain vless-tcp", resistant("vless", "a.example", "vless://b831381d-6324-4d53-ad4f-8cda48b30811@plain.example:443?security=tls&type=tcp#p"), false},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: throttleResistant = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// In automatic mode a throttle-resistant server ranks as if steelLead
// faster: a Cloudflare-fronted server a little slower than a plain one
// comes first, a plain one that is much faster still wins, and with a
// protocol chosen speed alone decides.
func TestSteelLeadPrefersResistant(t *testing.T) {
	e, err := New(Options{DataDir: t.TempDir(), Sources: []sources.Source{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.pool.Close() })
	now := time.Now()
	plain := "vless://b831381d-6324-4d53-ad4f-8cda48b30811@plain.example:443?security=tls&type=tcp#plain"
	cdn := "vless://b831381d-6324-4d53-ad4f-8cda48b30812@cdn.example:443?security=tls&type=ws&host=cdn.example&path=%2F#cdn"
	set := func(link, name string, delay int) {
		n, err := node.ParseLink(link)
		if err != nil {
			t.Fatal(err)
		}
		e.good[node.Key64(n.Key())] = &Server{Key: n.Key(), Link: link, Name: name, Proto: "vless", Country: "DE", Delay: delay, LastOK: now, TestedAt: now}
	}
	first := func() string {
		got := e.bestServers("DE", 2, nil)
		if len(got) == 0 {
			t.Fatal("no servers")
		}
		return got[0].Name
	}
	// A little slower (200 ms over): the resistant one comes first.
	set(plain, "plain", 100)
	set(cdn, "cdn", 300)
	if got := first(); got != "cdn" {
		t.Fatalf("automatic, resistant a little slower: %s first, want cdn", got)
	}
	// Much slower (400 ms over, beyond steelLead): the fast plain one wins.
	set(cdn, "cdn", 500)
	if got := first(); got != "plain" {
		t.Fatalf("automatic, resistant much slower: %s first, want plain", got)
	}
	// A chosen protocol: speed alone, even when the gap is small.
	set(cdn, "cdn", 300)
	e.mu.Lock()
	e.settings.Protocol = "vless"
	e.mu.Unlock()
	if got := first(); got != "plain" {
		t.Fatalf("chosen protocol: %s first, want plain", got)
	}
}

// serverKey returns the pool key of the good server with the given name.
func serverKey(t *testing.T, e *Engine, name string) uint64 {
	t.Helper()
	for k, s := range e.good {
		if s.Name == name {
			return k
		}
	}
	t.Fatalf("no server named %s", name)
	return 0
}
