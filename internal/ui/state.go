package ui

import "github.com/cezomagoanlace29861-jpg/vp/internal/engine"

// What the page is told. It shows how many servers can be reached, where
// they are and how fast; where servers come from, their names and how they
// are tested stay inside the engine.
type state struct {
	Status     string                  `json:"status"`
	Message    string                  `json:"message"`
	Target     string                  `json:"target"`
	TargetName string                  `json:"target_name"`
	TargetFlag string                  `json:"target_flag"`
	Conn       *conn                   `json:"conn"`
	Countries  []engine.CountryInfo    `json:"countries"`
	Servers    int                     `json:"servers"`
	Settings   publicSettings          `json:"settings"`
	Protocols  []engine.ProtocolChoice `json:"protocols"`
	Regions    []region                `json:"regions"`
	Admin      bool                    `json:"admin"`
	Mobile     bool                    `json:"mobile"`
	Version    string                  `json:"version"`
	Update     *engine.UpdateInfo      `json:"update,omitempty"`
	Account    engine.AccountState     `json:"account"`
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
	Protocol        string `json:"protocol"`
	BlockAds        bool   `json:"block_ads"`
	SecureOnly      bool   `json:"secure_only"`
	AutoConnect     bool   `json:"auto_connect"`
	StartWithSystem bool   `json:"start_with_system"`
}

func publicSettingsOf(s engine.Settings) publicSettings {
	return publicSettings{
		Mode: s.Mode, Bypass: s.Bypass, Protocol: s.Protocol, BlockAds: s.BlockAds,
		SecureOnly: s.SecureOnly, AutoConnect: s.AutoConnect, StartWithSystem: s.StartWithSystem,
	}
}

func publicState(s engine.Snapshot) state {
	out := state{
		Status: s.Status, Message: s.Message, Target: s.Target, TargetName: s.TargetName, TargetFlag: s.TargetFlag,
		Countries: s.Countries, Settings: publicSettingsOf(s.Settings), Protocols: engine.ProtocolChoices(),
		Admin: s.Admin, Mobile: s.Mobile, Version: s.Version, Update: s.Update, Account: s.Account,
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
