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
func bridgeLines(s Settings) []string {
	b := loadBuiltinBridges()
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
