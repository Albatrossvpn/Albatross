package engine

import (
	"testing"

	"github.com/Albatrossvpn/vp/internal/node"
	"github.com/Albatrossvpn/vp/internal/pool"
)

func TestRegimeClassify(t *testing.T) {
	for _, c := range []struct {
		p    regimeProbe
		want regime
	}{
		{regimeProbe{Blocked: true, Foreign: true, Google: true, RandomSNI: true, Domestic: true}, regimeFree},
		{regimeProbe{Foreign: true, Google: true, RandomSNI: true, Domestic: true, Poisoned: true}, regimeFiltered},
		{regimeProbe{Google: true, RandomSNI: true, Domestic: true}, regimeHard},
		{regimeProbe{Google: true, Domestic: true}, regimeAllowlist},
		{regimeProbe{Domestic: true}, regimeNational},
		{regimeProbe{}, regimeOffline},
	} {
		if got := c.p.classify(); got != c.want {
			t.Errorf("%+v: got %s, want %s", c.p, got, c.want)
		}
	}
}

func TestRegimeBonus(t *testing.T) {
	cdn := &node.Node{Proto: "vless", Server: "a.example", Port: 443, Security: "tls", Transport: "ws"}
	raw := &node.Node{Proto: "vless", Server: "1.2.3.4", Port: 31000, Security: "reality"}
	hy := &node.Node{Proto: "hysteria2", Server: "1.2.3.4", Port: 443}
	e := func(n *node.Node, flags uint8) *pool.Entry { return &pool.Entry{Style: n.Style(), Flags: flags} }
	p := regimeProbe{UDP: false}
	if b := regimeAllowlist.bonus(p, e(cdn, 0)); b <= 0 {
		t.Errorf("CDN server under the allowlist: %v", b)
	}
	if b := regimeAllowlist.bonus(p, e(raw, 0)); b >= 0 {
		t.Errorf("raw Reality server under the allowlist: %v", b)
	}
	if b := regimeFiltered.bonus(p, e(hy, pool.FlagUDP)); b >= 0 {
		t.Errorf("UDP server without UDP: %v", b)
	}
	if b := regimeFiltered.bonus(regimeProbe{UDP: true}, e(hy, pool.FlagUDP)); b != 0 {
		t.Errorf("UDP server with UDP: %v", b)
	}
}

func TestHelpOnlyOutsideIran(t *testing.T) {
	e := &Engine{}
	e.bin.Snowflake = "snowflake-helper"
	e.reg = regimeState{Regime: regimeFree, Probe: regimeProbe{Country: "DE"}}
	if !e.helpEligible() {
		t.Fatal("an open network in Germany should allow helping")
	}
	e.reg.Probe.Country = "IR"
	if e.helpEligible() {
		t.Fatal("helping must never run in Iran, even on an open network")
	}
	e.reg = regimeState{Regime: regimeFiltered, Probe: regimeProbe{Country: "DE"}}
	if e.helpEligible() {
		t.Fatal("helping needs an open network")
	}
}
