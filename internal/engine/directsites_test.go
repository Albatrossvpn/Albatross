package engine

import "testing"

func listHas(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// What the user types to name a site to open directly is cleaned to a bare
// domain (no scheme, path, port, "www." or "*."), kept once, and anything
// that is not a domain is dropped; the list is bounded.
func TestDirectSitesCleaned(t *testing.T) {
	s := DefaultSettings()
	s.DirectSites = []string{
		"https://www.Okala.com/path?x=1", "*.nobitex.ir", "nobitex.ir:443", " bale.ai ",
		"not a domain", "localhost", "-bad.com", "bad-.com", "", "okala.com",
	}
	s.normalize()
	want := []string{"okala.com", "nobitex.ir", "bale.ai"}
	if len(s.DirectSites) != len(want) {
		t.Fatalf("got %v, want %v", s.DirectSites, want)
	}
	for i, d := range want {
		if s.DirectSites[i] != d {
			t.Fatalf("got %v, want %v", s.DirectSites, want)
		}
	}
	many := make([]string, 0, directSitesMax+10)
	for i := 0; i < directSitesMax+10; i++ {
		many = append(many, "site"+string(rune('a'+i%26))+string(rune('a'+i/26))+".example")
	}
	s.DirectSites = many
	s.normalize()
	if len(s.DirectSites) != directSitesMax {
		t.Fatalf("%d sites kept, want at most %d", len(s.DirectSites), directSitesMax)
	}
}

// The system proxy's bypass list carries the user's sites, and their
// subdomains, beside the region's; with ads blocked everything goes through
// the proxy (where the core sends them direct and still blocks their ads).
func TestProxyBypassCarriesDirectSites(t *testing.T) {
	l := proxyBypass("IR", false, []string{"okala.com"})
	if !listHas(l, "okala.com") || !listHas(l, "*.okala.com") || !listHas(l, "*.ir") {
		t.Fatalf("bypass: %v", l)
	}
	if l := proxyBypass("", false, []string{"okala.com"}); !listHas(l, "okala.com") {
		t.Fatalf("no region, yet the user's site should be there: %v", l)
	}
	if l := proxyBypass("IR", true, []string{"okala.com"}); listHas(l, "okala.com") {
		t.Fatalf("ads blocked: the site should go through the proxy: %v", l)
	}
}
