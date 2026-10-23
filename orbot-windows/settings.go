package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Connection modes.
const (
	ModeProxy = "proxy" // set the Windows system proxy to Tor
	ModeVPN   = "vpn"   // route all traffic through a wintun adapter
	ModeSocks = "socks" // only open local SOCKS/HTTP ports
)

// Bridge choices.
const (
	BridgeNone      = "none"
	BridgeSnowflake = "snowflake"
	BridgeObfs4     = "obfs4"
	BridgeMeek      = "meek"
	BridgeCustom    = "custom"
)

type Settings struct {
	Mode          string `json:"mode"`
	Bridge        string `json:"bridge"`
	CustomBridges string `json:"customBridges"`
	ExitCountry   string `json:"exitCountry"`
	SocksPort     int    `json:"socksPort"`
	HTTPPort      int    `json:"httpPort"`
	DNSPort       int    `json:"dnsPort"`
	Lang          string `json:"lang"`

	// SavedProxy holds the user's previous WinINet proxy settings while
	// Orbot has replaced them, so a crash can be repaired on next start.
	SavedProxy *ProxyBackup `json:"savedProxy,omitempty"`
}

func defaultSettings() Settings {
	return Settings{
		Mode:      ModeProxy,
		Bridge:    BridgeNone,
		SocksPort: 9050,
		HTTPPort:  8118,
		DNSPort:   5400,
		Lang:      "fa",
	}
}

type SettingsStore struct {
	mu   sync.Mutex
	path string
	s    Settings
}

func loadSettings(dir string) *SettingsStore {
	st := &SettingsStore{path: filepath.Join(dir, "settings.json"), s: defaultSettings()}
	if b, err := os.ReadFile(st.path); err == nil {
		_ = json.Unmarshal(b, &st.s)
	}
	st.s.normalize()
	return st
}

func (s *Settings) normalize() {
	d := defaultSettings()
	switch s.Mode {
	case ModeProxy, ModeVPN, ModeSocks:
	default:
		s.Mode = d.Mode
	}
	switch s.Bridge {
	case BridgeNone, BridgeSnowflake, BridgeObfs4, BridgeMeek, BridgeCustom:
	default:
		s.Bridge = d.Bridge
	}
	if s.SocksPort <= 0 || s.SocksPort > 65535 {
		s.SocksPort = d.SocksPort
	}
	if s.HTTPPort <= 0 || s.HTTPPort > 65535 {
		s.HTTPPort = d.HTTPPort
	}
	if s.DNSPort <= 0 || s.DNSPort > 65535 {
		s.DNSPort = d.DNSPort
	}
	if s.Lang != "en" {
		s.Lang = "fa"
	}
}

func (st *SettingsStore) Get() Settings {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s
}

func (st *SettingsStore) Update(fn func(*Settings)) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	fn(&st.s)
	st.s.normalize()
	b, err := json.MarshalIndent(st.s, "", "  ")
	if err != nil {
		return err
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}
