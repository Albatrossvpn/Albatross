package engine

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/cezomagoanlace29861-jpg/vp/internal/sources"
)

// Settings are the user's choices, saved as settings.json.
type Settings struct {
	// Mode is "proxy" (system proxy, no admin needed) or "tun" (every app,
	// needs administrator rights).
	Mode string `json:"mode"`
	// Bypass is the code of a region (see regions.go) whose own sites are
	// opened directly instead of through the VPN; "" turns it off.
	Bypass string `json:"bypass"`
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
	// AllowPlain includes servers without transport encryption.
	AllowPlain bool `json:"allow_plain"`
	// Country is the last chosen country ("" means the fastest anywhere).
	Country string `json:"country"`
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
	// Warp also tries Cloudflare WARP, WARP in WARP and Psiphon.
	Warp bool `json:"warp"`
	// SpoofSNI, when set and running as administrator, sends a forged
	// ClientHello with this allowed name before the real one.
	SpoofSNI string `json:"spoof_sni"`
	// CleanIP retries Cloudflare-fronted servers through edge IPs that
	// pass on this network.
	CleanIP bool `json:"clean_ip"`
}

// ProtocolChoice is a connection protocol the user can pick.
type ProtocolChoice struct {
	Value string `json:"value"` // Settings.Protocol; "" is automatic
	Label string `json:"label"`
}

// ProtocolChoices are the protocols offered, automatic first. They match
// Server.Proto.
func ProtocolChoices() []ProtocolChoice {
	return []ProtocolChoice{
		{"", "Automatic"}, {"vless", "VLESS"}, {"vmess", "VMess"}, {"trojan", "Trojan"},
		{"shadowsocks", "Shadowsocks"}, {"hysteria2", "Hysteria 2"}, {"tuic", "TUIC"},
		{"anytls", "AnyTLS"}, {"wireguard", "WireGuard"}, {"openvpn", "OpenVPN"},
	}
}

// DefaultSettings favor reaching the internet while keeping traffic
// encrypted: servers without transport encryption are left out.
func DefaultSettings() Settings {
	return Settings{
		Mode:        "proxy",
		ProxyPort:   10808,
		AutoSwitch:  true,
		Fragment:    "auto",
		AllowPlain:  false,
		Intensity:   "normal",
		DeepSources: true,
		Warp:        true,
		CleanIP:     true,
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
		json.Unmarshal(b, &s)
	}
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
}
