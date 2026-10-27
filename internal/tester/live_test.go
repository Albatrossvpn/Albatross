package tester

import (
	"context"
	"net"
	"os"
	"regexp"
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
// PATH. Run on the server itself, the hops of a Hysteria 2 link cannot be
// checked: they are redirected only for packets that arrive from outside.
// Its own port is checked then, and elsewhere as well.
//
// To look into a failure: ALBATROSS_LIVE_ONLY keeps the links whose label
// holds one of its comma-separated words, and ALBATROSS_LIVE_LOG=1 adds
// what the cores and helpers print (through hide too).
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
		if n.PortHop != "" {
			one := *n
			one.PortHop = ""
			if localAddress(n.Server) {
				t.Logf("%s: the hops can be checked only from elsewhere", liveLabel(n))
				nodes[len(nodes)-1] = &one
			} else {
				nodes = append(nodes, &one)
			}
		}
	}
	if only := os.Getenv("ALBATROSS_LIVE_ONLY"); only != "" {
		var kept []*node.Node
		for _, n := range nodes {
			for _, w := range strings.Split(only, ",") {
				if w = strings.TrimSpace(w); w != "" && strings.Contains(liveLabel(n), w) {
					kept = append(kept, n)
					break
				}
			}
		}
		nodes = kept
	}
	if len(nodes) == 0 {
		t.Fatal("no links")
	}
	var dirs []string
	if d := os.Getenv("ALBATROSS_HELPERS"); d != "" {
		dirs = append(dirs, d)
	}
	rt := &core.Runtime{Bin: core.Locate(dirs...), Dir: t.TempDir()}
	if os.Getenv("ALBATROSS_LIVE_LOG") == "1" {
		rt.OnLine = func(name, line string) { t.Logf("%s| %s", name, hide(line, nodes)) }
	}
	tt := &Tester{Runtime: rt, Timeout: 15 * time.Second}
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

// localAddress reports whether host names this machine.
func localAddress(host string) bool {
	ips, err := net.LookupIP(host)
	if err != nil {
		return false
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok {
			for _, ip := range ips {
				if ipn.IP.Equal(ip) {
					return true
				}
			}
		}
	}
	return false
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
	if n.PortHop != "" {
		s += " hopping ports"
	}
	return s
}

// hide takes the servers' names, secrets and any address out of an
// error (a core's message may quote its config, or a resolved address).
func hide(s string, nodes []*node.Node) string {
	for _, n := range nodes {
		for _, v := range []string{n.Server, n.SNI, n.Host, n.VerifyName, n.UUID, n.Password, n.Username, n.Path, n.Seed,
			n.PublicKey, n.ShortID, n.HostKey, n.ObfsPassword, n.PrivateKey, n.PeerPublicKey, n.PreSharedKey} {
			if len(v) > 3 {
				s = strings.ReplaceAll(s, v, "<hidden>")
			}
		}
	}
	s = ipv4.ReplaceAllString(s, "<address>")
	// What looks like IPv6, but not a time of day.
	return ipv6.ReplaceAllStringFunc(s, func(m string) string {
		if strings.Contains(m, "::") || strings.ContainsAny(strings.ToLower(m), "abcdef") {
			return "<address>"
		}
		return m
	})
}

var (
	ipv4 = regexp.MustCompile(`\d{1,3}(\.\d{1,3}){3}`)
	ipv6 = regexp.MustCompile(`(?i)[0-9a-f]{0,4}(:[0-9a-f]{0,4}){2,7}`)
)

func TestHideTakesOutAddresses(t *testing.T) {
	n := &node.Node{Proto: "trojan", Server: "198-51-100-7.sslip.io", Password: "hunter22"}
	got := hide(`dial 198-51-100-7.sslip.io (198.51.100.7, [2001:db8::7]:443) as hunter22 at 13:45:17: refused`, []*node.Node{n})
	for _, leak := range []string{"198", "2001", "db8", "hunter22"} {
		if strings.Contains(got, leak) {
			t.Errorf("%q left in %q", leak, got)
		}
	}
	if !strings.Contains(got, "13:45:17") || !strings.Contains(got, "refused") {
		t.Errorf("too much taken out: %q", got)
	}
}

func TestLocalAddress(t *testing.T) {
	if !localAddress("127.0.0.1") || localAddress("192.0.2.1") {
		t.Error("the machine's own address is not told from another")
	}
}
