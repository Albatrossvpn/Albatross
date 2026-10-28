package ui

import "github.com/Albatrossvpn/vp/internal/engine"

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
	for _, p := range engine.ProtocolChoices() {
		c := s.ProtocolServers[p.Value]
		out.Protocols = append(out.Protocols, protocol{p, c[0], c[1]})
	}
	for _, c := range s.Countries {
		out.Servers += c.Servers
	}
	for _, r := range engine.Regions() {
		out.Regions = append(out.Regions, region{r.Code, r.Name})
	}
	if c := s.Conn; c != nil {
		out.Conn = &conn{Country: c.Country, CountryName: c.CountryName, Flag: c.Flag, Delay: c.Delay}
	}
	return out
}
