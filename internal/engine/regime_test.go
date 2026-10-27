package engine

import (
	"testing"
	"time"

	"github.com/Albatrossvpn/vp/internal/node"
	"github.com/Albatrossvpn/vp/internal/pool"
	"github.com/Albatrossvpn/vp/internal/sources"
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
	// A relay inside the country comes first where foreign addresses are
	// cut, whatever its protocol; a Hysteria on UDP 53 counts while UDP 53
	// is measured open, and nothing when it is not.
	if b := regimeNational.bonus(p, e(raw, pool.FlagDomestic)); b <= 100 {
		t.Errorf("domestic relay under national: %v", b)
	}
	hy53 := &node.Node{Proto: "hysteria2", Server: "1.2.3.4", Port: 53}
	if b := regimeNational.bonus(regimeProbe{UDP53: true}, e(hy53, pool.FlagUDP)); b < 60 {
		t.Errorf("Hysteria on 53 with UDP 53 open: %v", b)
	}
	if b := regimeNational.bonus(regimeProbe{}, e(hy53, pool.FlagUDP)); b > 0 {
		t.Errorf("Hysteria on 53 with UDP 53 cut: %v", b)
	}
	// The servers that can still get out rank far ahead under a shutdown.
	now := time.Now()
	relay := &Server{Domestic: true, Delay: 900, LastOK: now}
	far := &Server{Link: "vless://b831381d-6324-4d53-ad4f-8cda48b30811@1.2.3.4:443?security=reality&pbk=SbVKOEMjK0sIlbwg4akyBg5mL5KZwwB-ed4eEE7YnRc&sid=ab&type=tcp&fp=chrome&sni=a.com", Delay: 50, LastOK: now}
	if relay.rankIn(now, regimeNational, regimeProbe{}) >= far.rankIn(now, regimeNational, regimeProbe{}) {
		t.Error("a relay must rank ahead of a foreign server under national")
	}
	if relay.rankIn(now, regimeFiltered, regimeProbe{}) <= far.rankIn(now, regimeFiltered, regimeProbe{}) {
		t.Error("under ordinary filtering delay decides")
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

func TestCanGetOut(t *testing.T) {
	hy53, err := node.ParseLink("hysteria2://p@1.2.3.4:53?sni=x#hy53")
	if err != nil {
		t.Fatal(err)
	}
	vl, err := node.ParseLink("vless://00000000-0000-4000-8000-000000000000@1.2.3.4:443?security=tls&sni=a.example.com&type=tcp#r")
	if err != nil {
		t.Fatal(err)
	}
	e := func(n *node.Node, flags uint8) *pool.Entry { return &pool.Entry{Style: n.Style(), Flags: flags} }
	if !canGetOut(regimeNational, regimeProbe{}, e(vl, pool.FlagDomestic)) {
		t.Error("a relay inside the country always can")
	}
	if canGetOut(regimeNational, regimeProbe{}, e(vl, 0)) {
		t.Error("a foreign Reality server cannot where foreign addresses are cut")
	}
	if !canGetOut(regimeNational, regimeProbe{UDP53: true}, e(hy53, pool.FlagUDP)) || canGetOut(regimeNational, regimeProbe{}, e(hy53, pool.FlagUDP)) {
		t.Error("port 53 depends on UDP 53 getting out")
	}
}

// Only the project's own lists can name a relay: a public server called
// "x-relay" is no relay, while an address inside the country always is one.
func TestRelayByNameOnlyFromOwnLists(t *testing.T) {
	e, err := New(Options{DataDir: t.TempDir(), Sources: []sources.Source{}, Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.pool.Close()
	named := &node.Node{Proto: "vless", Server: "192.0.2.10", Port: 443, Name: "fast-relay"}
	if e.domesticEndpoint(named, false) {
		t.Error("a public server named as a relay was taken for one")
	}
	if !e.domesticEndpoint(named, true) {
		t.Error("a relay in the own list was not taken for one")
	}
	if len(e.irRanges) > 0 && !e.domesticEndpoint(&node.Node{Proto: "vless", Server: "5.160.0.1", Port: 443}, false) {
		t.Error("an address inside the country was not taken for one")
	}
}
