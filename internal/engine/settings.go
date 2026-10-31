package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/Albatrossvpn/vp/internal/sources"
)

// Settings are the user's choices, saved as settings.json.
type Settings struct {
	// Mode is "proxy" (system proxy, no admin needed) or "tun" (every app,
	// needs administrator rights).
	Mode string `json:"mode"`
	// Bypass is the code of a region (see regions.go) whose own sites are
	// opened directly instead of through the VPN; "" turns it off.
	Bypass string `json:"bypass"`
	// BypassApps leaves the bypass region's own apps outside the tunnel on
	// phones (DomesticApps): banks and other services that refuse a foreign
	// address or stop when a VPN is on keep working while connected.
	BypassApps bool `json:"bypass_apps"`
	// DirectSites are sites the user named to open outside the tunnel
	// (domains; each covers its subdomains): for the app or site that
	// refuses a VPN and is in no list, fixed in two taps rather than by
	// giving up on the app.
	DirectSites []string `json:"direct_sites"`
	// Protocol limits connections to one protocol ("" = automatic); see
	// ProtocolChoices.
	Protocol string `json:"protocol"`
	// BlockAds rejects connections to known ad and tracker domains.
	BlockAds bool `json:"block_ads"`
	// ProxyPort is the local HTTP+SOCKS port.
	ProxyPort int `json:"proxy_port"`
	// AllowLAN lets other devices on the network use the proxy port.
	AllowLAN bool `json:"allow_lan"`
	// AutoSwitch moves to clearly faster servers in the same country while
	// connected and refreshes backups that stopped working. Moving off a
	// server that failed happens either way.
	AutoSwitch bool `json:"auto_switch"`
	// Fragment controls TLS ClientHello fragmentation: "auto" retries
	// servers that fail with fragmentation on, "on" always fragments,
	// "off" never does.
	Fragment string `json:"fragment"`
	// HelpOthers runs a Snowflake proxy, which lets people in censored
	// countries reach Tor through this computer. It only runs while the
	// computer is outside Iran on an open network (see help.go).
	HelpOthers bool `json:"help_others"`
	// ShareStats shares, anonymously, which kinds of server pass on this
	// network and starts new networks from what others found (crowd.go).
	ShareStats bool `json:"share_stats"`
	// AllowPlain includes servers without transport encryption.
	AllowPlain bool `json:"allow_plain"`
	// SecureOnly refuses to send unencrypted protocols (plain HTTP, FTP,
	// telnet, mail without TLS) through the servers. Whoever runs a server
	// can read such traffic and change it; HTTPS stays encrypted from the
	// device to the site, whatever the server does.
	SecureOnly bool `json:"secure_only"`
	// Country is the last chosen country ("" means the fastest anywhere).
	Country string `json:"country"`
	// Connected says the user left the VPN on: the last thing they did was
	// connect, not disconnect. Android's Always-on VPN resumes it.
	Connected bool `json:"connected,omitempty"`
	// AutoConnect connects when the app starts.
	AutoConnect bool `json:"auto_connect"`
	// StartWithSystem launches the app at login.
	StartWithSystem bool `json:"start_with_system"`
	// Intensity is "low", "normal" or "max" scanning effort.
	Intensity string `json:"intensity"`
	// DeepSources also downloads the large lists needed for full coverage.
	DeepSources bool `json:"deep_sources"`
	// DisabledSources lists IDs of built-in sources the user turned off.
	DisabledSources []string `json:"disabled_sources"`
	// UserSources are subscriptions and channels the user added.
	UserSources []sources.Source `json:"user_sources"`
	// DomesticMirrors are mirror bases inside the country learned from
	// share codes (sources.DomesticBases), beside the built-in ones.
	DomesticMirrors []string `json:"domestic_mirrors,omitempty"`
	// Warp also tries Cloudflare WARP, WARP in WARP and Psiphon.
	Warp bool `json:"warp"`
	// SpoofSNI, when set and running as administrator, sends a forged
	// ClientHello with this allowed name before the real one.
	SpoofSNI string `json:"spoof_sni"`
	// CleanIP retries Cloudflare-fronted servers through edge IPs that
	// pass on this network.
	CleanIP bool `json:"clean_ip"`
	// OverrideGPS puts the phone's location in the VPN's country (Android).
	OverrideGPS bool `json:"override_gps"`
	// Version is the settingsVersion the file was last brought up to.
	Version int `json:"version,omitempty"`
}

// settingsVersion 1 turned the ad blocker on for everyone once: it was
// off by default before, so almost no one had chosen to leave it off.
const settingsVersion = 1

// ProtocolChoice is a connection protocol the user can pick.
type ProtocolChoice struct {
	Value string `json:"value"` // Settings.Protocol; "" is automatic
	Label string `json:"label"`
	// How the protocol behaves, for the icons beside it in the list (the
	// legend above the list explains them): Connect is how quickly it
	// connects and Speed how much it carries, each 1 (low) to 3 (high);
	// Shield says it is built to resist throttling (it rides a CDN or
	// disguises itself); UDP that it needs UDP, which mobile networks in
	// Iran often throttle; Resort that it is a slow last resort. Automatic
	// carries none: the list says what it is doing instead.
	Connect int  `json:"connect,omitempty"`
	Speed   int  `json:"speed,omitempty"`
	Shield  bool `json:"shield,omitempty"`
	UDP     bool `json:"udp,omitempty"`
	Resort  bool `json:"resort,omitempty"`
}

// ProtocolChoices are the protocols offered, automatic first. A value is
// a Server.Proto; "warp" and "tor" the two kinds of warpplus method; "dns"
// and "icmp" the carriers, a server reached through a DNS tunnel or inside
// ping (see Server.matches). Transports and TLS variants (Reality,
// WebSocket, gRPC, XHTTP, ShadowTLS, Salamander) are not protocols of
// their own: a VLESS server stays VLESS whichever carries it. The ratings
// are a protocol's usual character, not a promise: how fast it connects
// and how much it carries, whether it is built to resist throttling, and
// whether it needs UDP.
func ProtocolChoices() []ProtocolChoice {
	p := func(value, label string, connect, speed int, shield, udp bool) ProtocolChoice {
		return ProtocolChoice{Value: value, Label: label, Connect: connect, Speed: speed, Shield: shield, UDP: udp}
	}
	last := func(value, label string) ProtocolChoice {
		return ProtocolChoice{Value: value, Label: label, Connect: 1, Speed: 1, Shield: true, Resort: true}
	}
	return []ProtocolChoice{
		{Value: "", Label: "Automatic"},
		p("vless", "VLESS", 3, 3, false, false), p("vmess", "VMess", 3, 3, false, false), p("trojan", "Trojan", 3, 3, false, false),
		p("shadowsocks", "Shadowsocks", 3, 3, false, false), p("hysteria2", "Hysteria 2", 3, 3, false, true), p("hysteria", "Hysteria", 3, 3, false, true),
		p("tuic", "TUIC", 3, 3, false, true), p("anytls", "AnyTLS", 3, 3, false, false), p("wireguard", "WireGuard", 3, 3, false, true),
		p("amneziawg", "AmneziaWG", 3, 3, true, true), p("openvpn", "OpenVPN", 1, 2, false, false), p("ssh", "SSH", 2, 2, false, false),
		p("naive", "NaiveProxy", 3, 3, true, false), p("juicity", "Juicity", 3, 3, false, true), p("mieru", "Mieru", 2, 2, true, false),
		p("brook", "Brook", 3, 2, false, false), p("wstunnel", "WStunnel", 2, 2, false, false), p("ssr", "ShadowsocksR", 3, 2, false, false),
		p("snell", "Snell", 3, 3, false, false), p("socks", "SOCKS5", 3, 2, false, false), p("http", "HTTP proxy", 3, 2, false, false),
		p("warp", "Cloudflare WARP", 2, 3, true, false),
		last("tor", "Tor"),
		// The carriers are the ways through when every foreign address is
		// cut; the list offers them only while servers that use them are
		// known (ui/state.go), so they never stand there as dead choices.
		last("dns", "DNS tunnel"), last("icmp", "Ping tunnel"),
	}
}

// DefaultSettings favor reaching the internet while keeping traffic
// encrypted: servers without transport encryption are left out.
func DefaultSettings() Settings {
	return Settings{
		Mode: "proxy",
		// Iranian sites open directly: faster, it saves the servers'
		// bandwidth, and many of them refuse foreign visitors.
		Bypass:      "IR",
		BypassApps:  true,
		ProxyPort:   10808,
		AutoSwitch:  true,
		Fragment:    "auto",
		AllowPlain:  false,
		SecureOnly:  true,
		Intensity:   "normal",
		DeepSources: true,
		Warp:        true,
		CleanIP:     true,
		BlockAds:    true,
		Version:     settingsVersion,
	}
}

func loadSettings(dir string, mobile bool) Settings {
	s := DefaultSettings()
	if mobile {
		// On a phone, mobile data and battery matter more than covering
		// every list.
		s.DeepSources = false
		s.Intensity = "low"
	}
	b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err == nil {
		s.Version = 0
		json.Unmarshal(b, &s)
	}
	if s.Version < 1 {
		s.BlockAds = true
	}
	s.Version = settingsVersion
	if mobile {
		// "proxy" is the device-wide VPN there; there is no other mode.
		s.Mode = "proxy"
	}
	if s.ProxyPort <= 0 || s.ProxyPort > 65535 {
		s.ProxyPort = 10808
	}
	switch s.Mode {
	case "proxy", "tun":
	default:
		s.Mode = "proxy"
	}
	switch s.Fragment {
	case "auto", "on", "off":
	default:
		s.Fragment = "auto"
	}
	switch s.Intensity {
	case "low", "normal", "max":
	default:
		s.Intensity = "normal"
	}
	s.normalize()
	return s
}

func saveJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// normalize drops values that are not choices.
func (s *Settings) normalize() {
	if _, ok := findRegion(s.Bypass); !ok {
		s.Bypass = ""
	}
	known := false
	for _, c := range ProtocolChoices() {
		known = known || c.Value == s.Protocol
	}
	if !known {
		s.Protocol = ""
	}
	var mirrors []string
	for _, m := range s.DomesticMirrors {
		if m = strings.TrimRight(strings.TrimSpace(m), "/"); validMirror(m) && len(mirrors) < shareMirrorMax {
			mirrors = append(mirrors, m)
		}
	}
	s.DomesticMirrors = mirrors
	var sites []string
	seen := map[string]bool{}
	for _, d := range s.DirectSites {
		if d = cleanSite(d); d != "" && !seen[d] && len(sites) < directSitesMax {
			seen[d] = true
			sites = append(sites, d)
		}
	}
	s.DirectSites = sites
}

// directSitesMax bounds the sites a user names to open directly.
const directSitesMax = 100

// cleanSite turns what a user typed into a bare domain: no scheme, path,
// port, or leading "*." or "www."; "" when it is not a domain at all.
func cleanSite(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	s = strings.Trim(strings.TrimPrefix(strings.TrimPrefix(s, "*."), "www."), ".")
	if !validSite(s) {
		return ""
	}
	return s
}

// validSite reports whether s is a domain: labels of letters, digits and
// hyphens, at least one dot.
func validSite(s string) bool {
	if len(s) < 4 || len(s) > 253 || !strings.Contains(s, ".") {
		return false
	}
	for _, l := range strings.Split(s, ".") {
		if l == "" || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for _, c := range l {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
