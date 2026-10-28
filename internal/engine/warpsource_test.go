package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Albatrossvpn/vp/internal/node"
	"github.com/Albatrossvpn/vp/internal/sources"
)

// The device's WARP account makes AmneziaWG and WireGuard servers that run
// on their usual engines, keep WireGuard's message types (WARP drops what
// it does not know, so only the junk packets before the handshake may
// differ) and never go into a share code.
func TestWarpSource(t *testing.T) {
	w := &warpID{
		PrivateKey: "cPr+XL8mZ2rA2lD8Q1D8Q1D8Q1D8Q1D8Q1D8Q1D8Q1E=",
		PeerKey:    "bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=",
		V4:         "172.16.0.2", V6: "2606:4700:110:8a36::2", Reserved: []int{1, 2, 3},
	}
	text := w.links()
	if strings.Count(text, "\n") != 18 || text != w.links() {
		t.Fatalf("links:\n%s", text)
	}
	protos := map[string]int{}
	ends := map[string]bool{}
	node.EachText(text, func(n *node.Node) {
		protos[n.Proto]++
		ends[n.Endpoint()] = true
		if n.PeerPublicKey != w.PeerKey || len(n.LocalAddress) != 2 || n.MTU != 1280 {
			t.Errorf("%s: peer %q addresses %v mtu %d", n.Name, n.PeerPublicKey, n.LocalAddress, n.MTU)
		}
		switch n.Proto {
		case "amneziawg":
			a := n.AWG
			if n.Engine() != node.EngineSidecar || a.Jc == 0 || a.S1 != 0 || a.S2 != 0 || a.H1 != "" || a.H4 != "" {
				t.Errorf("%s: %+v", n.Name, a)
			}
		case "wireguard":
			if len(n.Reserved) != 3 {
				t.Errorf("%s: reserved %v", n.Name, n.Reserved)
			}
		}
	})
	if protos["amneziawg"] != 12 || protos["wireguard"] != 6 || len(ends) < 10 {
		t.Fatalf("protocols %v, %d endpoints", protos, len(ends))
	}

	// The fetcher serves them as a source; without a device there is none.
	f := &sources.Fetcher{CacheDir: t.TempDir(), Warp: func(context.Context) (string, error) { return text, nil }}
	if res := f.Fetch(context.Background(), sources.Warp()); res.Err != nil || res.Text != text {
		t.Fatalf("fetch: %v", res.Err)
	}
	if res := (&sources.Fetcher{}).Fetch(context.Background(), sources.Warp()); res.Err == nil {
		t.Fatal("a fetcher without a device made WARP servers")
	}

	e, err := New(Options{DataDir: t.TempDir(), Sources: []sources.Source{}})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	var link string
	node.EachText(text, func(n *node.Node) {
		if link == "" {
			link = n.Link()
		}
	})
	e.mu.Lock()
	e.good[1] = &Server{Key: "w", key64: 1, Link: link, Name: "WARP", Source: sources.Warp().Label(), LastOK: time.Now(), Delay: 50}
	e.mu.Unlock()
	if code, err := e.ShareCode(); err == nil {
		t.Fatalf("the device's WARP server went into a share code: %q", code)
	}
}
