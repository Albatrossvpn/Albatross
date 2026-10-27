package tester

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Albatrossvpn/vp/internal/core"
	"github.com/Albatrossvpn/vp/internal/node"
)

// TestLiveLinks connects through each of a server's links with the app's
// own cores and helpers and makes a real request through it: the check,
// on the project's own server, that the links it prints work. It is
// opt-in, and names no address or secret, since the log of a server.yml
// run is public:
//
//	ALBATROSS_LIVE_LINKS=/root/albatross-links.txt ALBATROSS_HELPERS=/path/helpers go test -run LiveLinks ./internal/tester
//
// The cores and helpers are looked for in ALBATROSS_HELPERS, then on the
// PATH. Run on the server itself, a Hysteria 2 link that hops ports fails:
// the hops are redirected only for packets that arrive from outside.
func TestLiveLinks(t *testing.T) {
	path := os.Getenv("ALBATROSS_LIVE_LINKS")
	if path == "" {
		t.Skip("ALBATROSS_LIVE_LINKS not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var nodes []*node.Node
	for _, l := range node.ExtractLinks(string(raw)) {
		n, err := node.ParseLink(l)
		if err != nil {
			scheme, _, _ := strings.Cut(l, "://")
			t.Errorf("%s: a link the app does not read", scheme)
			continue
		}
		nodes = append(nodes, n)
	}
	if len(nodes) == 0 {
		t.Fatal("no links")
	}
	var dirs []string
	if d := os.Getenv("ALBATROSS_HELPERS"); d != "" {
		dirs = append(dirs, d)
	}
	tt := &Tester{Runtime: &core.Runtime{Bin: core.Locate(dirs...), Dir: t.TempDir()}, Timeout: 15 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var mu sync.Mutex
	results := map[string]Result{}
	err = tt.Batch(ctx, "live", nodes, func(r Result) {
		mu.Lock()
		results[r.Node.Key()] = r
		mu.Unlock()
	})
	if err != nil {
		t.Errorf("the cores: %s", hide(err.Error(), nodes))
	}
	for _, n := range nodes {
		what := liveLabel(n)
		switch r, ok := results[n.Key()]; {
		case !ok:
			t.Errorf("%s: not tested", what)
		case r.OK:
			t.Logf("%s: works (%v)", what, r.Delay.Round(time.Millisecond))
		default:
			t.Errorf("%s: %s", what, hide(r.Err, nodes))
		}
	}
}

// liveLabel tells a link apart from the server's others without its
// address or secrets.
func liveLabel(n *node.Node) string {
	s := n.Proto
	for _, p := range []string{n.Transport, n.Security} {
		if p != "" {
			s += "/" + p
		}
	}
	if n.Sidecar() != "" {
		s += " (" + n.Sidecar() + ")"
	}
	return s
}

// hide takes the servers' names and addresses out of an error.
func hide(s string, nodes []*node.Node) string {
	for _, n := range nodes {
		for _, v := range []string{n.Server, n.SNI, n.Host, n.VerifyName} {
			if len(v) > 3 {
				s = strings.ReplaceAll(s, v, "<server>")
			}
		}
	}
	return s
}
