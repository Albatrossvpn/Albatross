package engine

import (
	"strings"
	"testing"

	"github.com/Albatrossvpn/vp/internal/node"
)

// The WARP methods and Tor are two protocols of their own: choosing one must
// start it (nothing else would), choosing any other protocol must leave both
// out, and Automatic runs both while the setting for them is on.
func TestMethodsWanted(t *testing.T) {
	for _, c := range []struct {
		protocol  string
		warpOn    bool
		warp, tor bool
	}{
		{"", true, true, true},
		{"", false, false, false},
		{"warp", true, true, false},
		{"warp", false, true, false}, // chosen: the setting for Automatic does not matter
		{"tor", true, false, true},
		{"tor", false, false, true},
		{"vless", true, false, false},
		{"hysteria2", true, false, false},
		{"wireguard", false, false, false},
	} {
		warp, tor := methodsWanted(Settings{Protocol: c.protocol, Warp: c.warpOn})
		if warp != c.warp || tor != c.tor {
			t.Errorf("protocol %q, warp setting %v: got warp=%v tor=%v, want warp=%v tor=%v", c.protocol, c.warpOn, warp, tor, c.warp, c.tor)
		}
	}
}

// A server knows which warp-plus method it runs and whether it travels
// through a DNS tunnel, so the screen names what it really is.
func TestServerMethodAndCarrier(t *testing.T) {
	for _, mode := range []string{"tor", "warp", "gool", "masque", "masque2"} {
		s := &Server{Proto: "warpplus", Link: node.WarpPlus(mode, "").Link()}
		if got := s.method(); got != mode {
			t.Errorf("method of %s = %q", mode, got)
		}
		if got := s.carrier(); got != "" {
			t.Errorf("a %s method has no carrier, got %q", mode, got)
		}
	}
	ps := &Server{Proto: "warpplus", Link: node.WarpPlus("psiphon", "de").Link()}
	if ps.method() != "psiphon" {
		t.Errorf("psiphon: %q", ps.method())
	}
	plain := &Server{Proto: "vless", Link: "vless://007a5c24-cdbe-4adc-a79f-ca2768fef8bb@203.0.113.9:443?security=tls&sni=example.org&type=tcp#x"}
	if plain.method() != "" || plain.carrier() != "" {
		t.Errorf("a plain server has neither: %q %q", plain.method(), plain.carrier())
	}
}

// With a protocol chosen, the search line names that protocol and never says
// the app is trying the DNS tunnel or the relays, which it does not try.
func TestSearchMessage(t *testing.T) {
	auto := &Engine{}
	if got := auto.searchMessage("Germany", true, false); !strings.Contains(got, "DNS tunnel") {
		t.Errorf("Automatic in a shutdown still says what it tries: %q", got)
	}
	if got := auto.searchMessage("Germany", false, false); got != "Looking for a server in Germany…" {
		t.Errorf("plain search: %q", got)
	}
	if got := auto.searchMessage("Germany", false, true); !strings.HasPrefix(got, "No server in Germany yet.") {
		t.Errorf("late search: %q", got)
	}
	chosen := &Engine{settings: Settings{Protocol: "tuic"}}
	if got := chosen.searchMessage("Germany", false, false); got != "Looking for a TUIC server in Germany…" {
		t.Errorf("chosen search: %q", got)
	}
	if got := chosen.searchMessage("Germany", false, true); !strings.Contains(got, "No TUIC server works in Germany yet") {
		t.Errorf("chosen, late: %q", got)
	}
	got := chosen.searchMessage("Germany", true, false)
	if !strings.Contains(got, "TUIC cannot connect right now") || !strings.Contains(got, "Choose Automatic") {
		t.Errorf("chosen in a shutdown: %q", got)
	}
	if strings.Contains(got, "Trying the") {
		t.Errorf("a chosen protocol must not claim it is trying the tunnels: %q", got)
	}
	tor := &Engine{settings: Settings{Protocol: "tor"}}
	if got := tor.searchMessage("the fastest location", false, false); got != "Looking for a Tor server in the fastest location…" {
		t.Errorf("tor: %q", got)
	}
}

// A protocol is missing on a device when the program it runs on is not
// installed there; the built-in methods count by their own programs.
func TestMissingProtocols(t *testing.T) {
	e := &Engine{}
	none := map[string]bool{}
	for _, v := range e.missingProtocols() {
		none[v] = true
	}
	for _, v := range []string{"warp", "tor", "naive", "juicity", "mieru", "brook", "wstunnel", "amneziawg", "ssr", "snell"} {
		if !none[v] {
			t.Errorf("%s needs a program a bare engine does not have", v)
		}
	}
	for _, v := range []string{"", "vless", "vmess", "trojan", "shadowsocks", "hysteria2", "hysteria", "tuic", "anytls", "wireguard", "openvpn", "ssh", "socks", "http"} {
		if none[v] {
			t.Errorf("%s runs on sing-box or Xray, never a helper", v)
		}
	}
	e.bin.Sidecars = map[string]string{"brook": "/x/brook", "mihomo": "/x/mihomo"}
	e.bin.Tor = "/x/tor"
	none = map[string]bool{}
	for _, v := range e.missingProtocols() {
		none[v] = true
	}
	for _, v := range []string{"brook", "snell", "ssr", "tor"} {
		if none[v] {
			t.Errorf("%s has its program here", v)
		}
	}
	if !none["wstunnel"] || !none["warp"] {
		t.Errorf("wstunnel and warp are still missing: %v", none)
	}
}
