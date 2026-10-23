package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ptConfig is the Tor Expert Bundle's pluggable_transports/pt_config.json:
// the ClientTransportPlugin lines and built-in bridges for that release.
type ptConfig struct {
	PluggableTransports map[string]string   `json:"pluggableTransports"`
	Bridges             map[string][]string `json:"bridges"`
}

func (p TorPaths) loadPTConfig() *ptConfig {
	if p.PTDir == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(p.PTDir, "pt_config.json"))
	if err != nil {
		return nil
	}
	var c ptConfig
	if json.Unmarshal(b, &c) != nil {
		return nil
	}
	return &c
}

var pluginLineRe = regexp.MustCompile(`^ClientTransportPlugin\s+(\S+)\s+exec\s+\$\{pt_path\}(\S+?)\$\{pt_ext\}(.*)$`)

// transportPlugins returns the ClientTransportPlugin lines for the bundled
// transports and the set of transport names they provide.
func (t *Tor) transportPlugins() ([]string, map[string]bool) {
	have := map[string]bool{}
	var lines []string
	add := func(names, binary, args string) {
		exe, ok := t.paths.ptExec(binary)
		if !ok {
			return
		}
		lines = append(lines, strings.TrimRight("ClientTransportPlugin "+names+" exec "+exe+args, " "))
		for _, n := range strings.Split(names, ",") {
			have[strings.ToLower(n)] = true
		}
	}

	if c := t.paths.loadPTConfig(); c != nil {
		for _, line := range c.PluggableTransports {
			if m := pluginLineRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
				add(m[1], m[2], m[3])
			}
		}
	}
	if len(lines) > 0 {
		return lines, have
	}

	// Older bundles without pt_config.json.
	add("meek_lite,obfs2,obfs3,obfs4,scramblesuit,webtunnel", "lyrebird", "")
	add("snowflake", "snowflake-client", "")
	add("conjure", "conjure-client", " -registerURL https://registration.refraction.network/api")
	return lines, have
}
