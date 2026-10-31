package ui

import (
	"sort"

	"github.com/Albatrossvpn/vp/internal/engine"
)

// What the page is told. It shows how many servers can be reached, where
// they are and how fast; where servers come from, their names and how they
// are tested stay inside the engine.
type state struct {
	Status     string               `json:"status"`
	Message    string               `json:"message"`
	Target     string               `json:"target"`
	TargetName string               `json:"target_name"`
	TargetFlag string               `json:"target_flag"`
	Conn       *conn                `json:"conn"`
	Countries  []engine.CountryInfo `json:"countries"`
	Servers    int                  `json:"servers"`
	Settings   publicSettings       `json:"settings"`
	Protocols  []protocol           `json:"protocols"`
	Regions    []region             `json:"regions"`
	Admin      bool                 `json:"admin"`
	Mobile     bool                 `json:"mobile"`
	Version    string               `json:"version"`
	Update     *engine.UpdateInfo   `json:"update,omitempty"`
	Account    engine.AccountState  `json:"account"`
	// Share is where other devices reach the proxy (Settings.AllowLAN).
	Share    []string `json:"share,omitempty"`
	SharePAC []string `json:"share_pac,omitempty"`
	// Notice is an important message for the user (engine.Snapshot).
	Notice         string `json:"notice,omitempty"`
	HelpAvailable  bool   `json:"help_available"`
	Helping        bool   `json:"helping"`
	CrowdAvailable bool   `json:"crowd_available"`
	// LocationSetup is what the phone still needs before Albatross may set
	// its location (Hooks.LocationSetup), while OverrideGPS is on.
	LocationSetup string `json:"location_setup,omitempty"`
}

type conn struct {
	Country     string `json:"country"`
	CountryName string `json:"country_name"`
	Flag        string `json:"flag"`
	Delay       int    `json:"delay"`
	// Protocol names how the server is reached ("VLESS Reality"), Since
	// when the connection began (Unix seconds); Up and Down are the bytes
	// it carried, UpRate and DownRate its bytes a second now.
	Protocol string `json:"protocol"`
	Since    int64  `json:"since"`
	Up       int64  `json:"up"`
	Down     int64  `json:"down"`
	UpRate   int64  `json:"up_rate"`
	DownRate int64  `json:"down_rate"`
}

// otherProtocols are the names of what the protocol list does not offer.
var otherProtocols = map[string]string{
	"direct": "Direct", "warpplus": "WARP+", "psiphon": "Psiphon", "masque": "MASQUE", "masque2": "MASQUE", "gool": "WARP in WARP",
}

// protocolName is how a protocol is shown: its name in the protocol list,
// with REALITY, the TLS that looks like a well-known site, named.
func protocolName(proto, security string) string {
	name := proto
	if n, ok := otherProtocols[proto]; ok {
		name = n
	}
	for _, p := range engine.ProtocolChoices() {
		if p.Value == proto && p.Value != "" {
			name = p.Label
		}
	}
	if security == "reality" {
		name += " Reality"
	}
	return name
}

// methodNames are the warp-plus methods as the screen names them, and
// carrierNames the ways through that are not protocols of their own.
var (
	methodNames = map[string]string{
		"warp": "Cloudflare WARP", "gool": "WARP in WARP", "psiphon": "Psiphon",
		"masque": "WARP · MASQUE", "masque2": "WARP · MASQUE (TCP)", "tor": "Tor",
	}
	carrierNames = map[string]string{"dns": "DNS tunnel", "icmp": "ping tunnel"}
)

// connLabel is how a connection is named: its protocol, or its method for
// warp-plus (Tor is not "WARP+"), and the carrier when it is not the plain
// network, so a server reached through a DNS tunnel never passes for the
// protocol it carries.
func connLabel(c *engine.ConnInfo) string {
	name := protocolName(c.Proto, c.Security)
	if m, ok := methodNames[c.Method]; ok {
		name = m
	}
	if car, ok := carrierNames[c.Carrier]; ok {
		name += " · " + car
	}
	return name
}

type region struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// publicSettings are the settings the page can change.
type publicSettings struct {
	Mode            string `json:"mode"`
	Bypass          string `json:"bypass"`
	BypassApps      bool   `json:"bypass_apps"`
	Protocol        string `json:"protocol"`
	BlockAds        bool   `json:"block_ads"`
	SecureOnly      bool   `json:"secure_only"`
	AutoConnect     bool   `json:"auto_connect"`
	StartWithSystem bool   `json:"start_with_system"`
	AllowLAN        bool   `json:"allow_lan"`
	HelpOthers      bool   `json:"help_others"`
	ShareStats      bool   `json:"share_stats"`
	OverrideGPS     bool   `json:"override_gps"`
}

// protocol is a protocol to choose, with how many of its servers are not
// known dead and how many worked (the methods, WARP and Tor, have none).
type protocol struct {
	engine.ProtocolChoice
	Servers int `json:"servers,omitempty"`
	Working int `json:"working,omitempty"`
	// Failed counts the servers tried that failed; Missing says this device
	// cannot run the protocol at all.
	Failed  int  `json:"failed,omitempty"`
	Missing bool `json:"missing,omitempty"`
	// Now, on Automatic alone, names what it is connected through right
	// now, so the choice it made is never a mystery.
	Now string `json:"now,omitempty"`
}

func publicSettingsOf(s engine.Settings) publicSettings {
	return publicSettings{
		Mode: s.Mode, Bypass: s.Bypass, BypassApps: s.BypassApps, Protocol: s.Protocol, BlockAds: s.BlockAds,
		SecureOnly: s.SecureOnly, AutoConnect: s.AutoConnect, StartWithSystem: s.StartWithSystem,
		AllowLAN: s.AllowLAN, HelpOthers: s.HelpOthers, ShareStats: s.ShareStats, OverrideGPS: s.OverrideGPS,
	}
}

func publicState(s engine.Snapshot) state {
	out := state{
		Status: s.Status, Message: s.Message, Target: s.Target, TargetName: s.TargetName, TargetFlag: s.TargetFlag,
		Countries: s.Countries, Settings: publicSettingsOf(s.Settings),
		Admin: s.Admin, Mobile: s.Mobile, Version: s.Version, Update: s.Update, Account: s.Account,
		Share: s.Share, SharePAC: s.SharePAC,
		Notice: s.Notice, HelpAvailable: s.HelpAvailable, Helping: s.Helping, CrowdAvailable: s.CrowdAvailable,
	}
	missing := map[string]bool{}
	for _, v := range s.ProtocolMissing {
		missing[v] = true
	}
	for _, p := range engine.ProtocolChoices() {
		c := s.ProtocolServers[p.Value]
		if p.Resort && p.Value != "tor" && c[0] == 0 {
			// The carriers (DNS tunnel, ping) are offered only while servers
			// that use them exist; a dead choice only confuses.
			continue
		}
		pr := protocol{ProtocolChoice: p, Servers: c[0], Working: c[1], Failed: s.ProtocolFailed[p.Value], Missing: missing[p.Value]}
		if p.Value == "" && s.Settings.Protocol == "" && s.Conn != nil {
			pr.Now = connLabel(s.Conn)
		}
		out.Protocols = append(out.Protocols, pr)
	}
	// Automatic first; then what works on this network, then what has
	// servers at all; the usual order among equals.
	sort.SliceStable(out.Protocols, func(i, j int) bool {
		a, b := out.Protocols[i], out.Protocols[j]
		if (a.Value == "") != (b.Value == "") {
			return a.Value == ""
		}
		if a.Working != b.Working {
			return a.Working > b.Working
		}
		return a.Servers > b.Servers
	})
	for _, c := range s.Countries {
		out.Servers += c.Servers
	}
	for _, r := range engine.Regions() {
		out.Regions = append(out.Regions, region{r.Code, r.Name})
	}
	if c := s.Conn; c != nil {
		out.Conn = &conn{Country: c.Country, CountryName: c.CountryName, Flag: c.Flag, Delay: c.Delay,
			Protocol: connLabel(c), Since: c.Since,
			Up: c.Up, Down: c.Down, UpRate: c.UpRate, DownRate: c.DownRate}
	}
	return out
}
