package tester

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Albatrossvpn/vp/internal/core"
	"github.com/Albatrossvpn/vp/internal/node"
)

// installerDryRun runs deploy/server/install.sh in its dry run, which
// writes a server's configs into the returned directory and prints its
// links: here a server with a DNS tunnel and a Cloudflare name, its
// secrets made with the real cores, at a documentation address (the app
// refuses links to the loopback one) that no test connects to.
func installerDryRun(t *testing.T, sb, xr string) (dir string, links []string) {
	dir = t.TempDir()
	cmd := exec.Command("bash", filepath.Join("..", "..", "deploy", "server", "install.sh"))
	cmd.Env = append(os.Environ(), "DRY_RUN=1", "OUT="+dir, "IP=192.0.2.1", "IP6=2001:db8::1", "DOMAIN=test.example",
		"SIDECARS=0", "DNS_TUNNEL=t.test.example", "CDN_HOST=cdn.test.example", "MIRROR=0", "RELAY_TO=",
		"SB="+sb, "XRAY="+xr)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("install.sh: %v\n%s", err, stderr.String())
	}
	return dir, node.ExtractLinks(string(out))
}

// TestInstallerDNSTunnel connects through the DNS tunnel install.sh sets
// up: its Xray inbound, from the config the script writes, on a free port,
// and its link through the app's core, the queries going straight to the
// server. An answer holds 512 bytes unless both sides say more; the
// server drops a packet its answer cannot hold, and the tunnel stalls.
//
//	SING_BOX=... XRAY=... E2E_UPSTREAM=direct go test -run InstallerDNSTunnel ./internal/tester
func TestInstallerDNSTunnel(t *testing.T) {
	sb, xr, up := os.Getenv("SING_BOX"), os.Getenv("XRAY"), os.Getenv("E2E_UPSTREAM")
	if sb == "" || xr == "" || up == "" {
		t.Skip("SING_BOX, XRAY and E2E_UPSTREAM must be set")
	}
	dir, links := installerDryRun(t, sb, xr)
	b, err := os.ReadFile(filepath.Join(dir, "xray.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	var in map[string]any
	for _, i := range cfg.Inbounds {
		if i["tag"] == "xdns" {
			in = i
		}
	}
	if in == nil {
		t.Fatal("no DNS tunnel inbound in xray.json")
	}
	// Alone: the server's other inbounds need root (port 53, ICMP).
	ports, err := core.FreePorts(1)
	if err != nil {
		t.Fatal(err)
	}
	in["listen"], in["port"] = "127.0.0.1", ports[0]
	out := map[string]any{"protocol": "freedom"}
	if up != "direct" {
		u, _ := url.Parse(up)
		host, port, _ := net.SplitHostPort(u.Host)
		p, _ := strconv.Atoi(port)
		out = map[string]any{"protocol": "http", "settings": map[string]any{"servers": []any{map[string]any{"address": host, "port": p}}}}
	}
	startServer(t, xr, dir, "dns-server.json", map[string]any{
		"log": map[string]any{"loglevel": "warning"}, "inbounds": []any{in}, "outbounds": []any{out},
	}, "run", "-c")
	waitDNS(t, "127.0.0.1:"+strconv.Itoa(ports[0]))

	var n *node.Node
	for _, l := range links {
		if x, err := node.ParseLink(l); err == nil && x.DNSTunnel() {
			n = x
		}
	}
	if n == nil {
		t.Fatal("install.sh printed no DNS tunnel link")
	}
	// The queries go to the server itself instead of through resolvers.
	var fm map[string]any
	if err := json.Unmarshal([]byte(n.FinalMask), &fm); err != nil {
		t.Fatal(err)
	}
	for _, m := range fm["udp"].([]any) {
		if mask := m.(map[string]any); mask["type"] == "xdns" {
			mask["settings"].(map[string]any)["resolvers"] = []any{map[string]any{"addrs": []any{"udp://127.0.0.1:" + strconv.Itoa(ports[0])}}}
		}
	}
	b, _ = json.Marshal(fm)
	n.FinalMask = string(b)

	tst := &Tester{Runtime: &core.Runtime{Bin: core.Binaries{SingBox: sb, Xray: xr}, Dir: dir}, Timeout: 15 * time.Second}
	var r Result
	if err := tst.Batch(context.Background(), "dns", []*node.Node{n}, func(x Result) { r = x }); err != nil {
		t.Fatal(err)
	}
	if !r.OK {
		t.Fatalf("no request went through the DNS tunnel: %s", r.Err)
	}
	t.Logf("through the DNS tunnel in %v", r.Delay.Round(time.Millisecond))
}

// waitDNS waits for a DNS server at addr to answer, as the tunnel's does
// once it listens (a name not its own with NXDOMAIN).
func waitDNS(t *testing.T, addr string) {
	query := []byte{0, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 1, 'x', 0, 0, 1, 0, 1}
	for range 100 {
		if c, err := net.Dial("udp", addr); err == nil {
			c.SetDeadline(time.Now().Add(100 * time.Millisecond))
			c.Write(query)
			_, err = c.Read(make([]byte, 512))
			c.Close()
			if err == nil {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the DNS tunnel server does not answer")
}

// TestInstallerCDNLinks checks the links install.sh prints for its
// Cloudflare name: the edge carries WebSocket and HTTPUpgrade on each of
// its HTTPS ports, but gRPC on 443 alone, which the server's REALITY
// holds, so a gRPC link there could never connect.
func TestInstallerCDNLinks(t *testing.T) {
	sb, xr := os.Getenv("SING_BOX"), os.Getenv("XRAY")
	if sb == "" || xr == "" {
		t.Skip("SING_BOX and XRAY must be set")
	}
	_, links := installerDryRun(t, sb, xr)
	https := map[int]bool{443: true, 2053: true, 2083: true, 2087: true, 2096: true, 8443: true}
	seen := 0
	for _, l := range links {
		n, err := node.ParseLink(l)
		if err != nil || n.Server != "cdn.test.example" {
			continue
		}
		seen++
		if !https[n.Port] {
			t.Errorf("%s: Cloudflare proxies no HTTPS on port %d", n.Name, n.Port)
		}
		if n.Transport != "ws" && n.Transport != "httpupgrade" {
			t.Errorf("%s: %s does not go through Cloudflare on port %d", n.Name, n.Transport, n.Port)
		}
	}
	if seen == 0 {
		t.Fatal("install.sh printed no link through the Cloudflare name")
	}
}
