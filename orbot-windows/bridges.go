package main

import (
	_ "embed"
	"encoding/json"
	"strings"
)

// builtin-bridges.json is taken from upstream Orbot
// (app/src/main/assets/builtin-bridges.json, BSD-3-Clause).
//
//go:embed assets/builtin-bridges.json
var builtinBridgesJSON []byte

type builtinBridges struct {
	Meek      []string `json:"meek"`
	Obfs4     []string `json:"obfs4"`
	Snowflake []string `json:"snowflake"`
}

func loadBuiltinBridges() builtinBridges {
	var b builtinBridges
	_ = json.Unmarshal(builtinBridgesJSON, &b)
	return b
}

// bridgeLines returns the Bridge lines for the chosen bridge type.
// Built-in bridges come from the bundle's pt_config.json when it has them,
// since that ships with each Tor release, and otherwise from Orbot's list.
func bridgeLines(s Settings, pt *ptConfig) []string {
	b := loadBuiltinBridges()
	if pt != nil {
		if v := pt.Bridges["snowflake"]; len(v) > 0 {
			b.Snowflake = v
		}
		if v := pt.Bridges["obfs4"]; len(v) > 0 {
			b.Obfs4 = v
		}
		if v := pt.Bridges["meek"]; len(v) > 0 {
			b.Meek = v
		} else if v := pt.Bridges["meek-azure"]; len(v) > 0 {
			b.Meek = v
		}
	}
	switch s.Bridge {
	case BridgeSnowflake:
		return b.Snowflake
	case BridgeObfs4:
		return b.Obfs4
	case BridgeMeek:
		return b.Meek
	case BridgeCustom:
		return parseCustomBridges(s.CustomBridges)
	}
	return nil
}

// parseCustomBridges accepts the text users copy from bridges.torproject.org
// or the Tor Telegram bot: one bridge per line, optionally prefixed "Bridge".
func parseCustomBridges(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(line) > 7 && strings.EqualFold(line[:7], "bridge ") {
			line = strings.TrimSpace(line[7:])
		}
		// Reject anything that could inject extra torrc options.
		if strings.ContainsAny(line, "\r\x00") {
			continue
		}
		out = append(out, line)
	}
	return out
}
