package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestRegionRanges(t *testing.T) {
	for _, c := range []struct {
		code  string
		local []string
	}{
		{"IR", []string{"185.143.232.10", "2.176.0.1"}}, // ArvanCloud, TCI
		{"CN", []string{"114.114.114.114", "223.5.5.5", "39.156.66.10"}},
	} {
		r, ok := findRegion(c.code)
		if !ok {
			t.Fatalf("no region %s", c.code)
		}
		ranges, err := readIPRuleSet(r.geoIPFile("rules"))
		if err != nil {
			t.Fatalf("%s: %v", c.code, err)
		}
		if len(ranges) < 500 {
			t.Fatalf("%s: only %d ranges", c.code, len(ranges))
		}
		for _, ip := range c.local {
			if !ipIn(ranges, netip.MustParseAddr(ip)) {
				t.Errorf("%s: %s should be local", c.code, ip)
			}
		}
		for _, ip := range []string{"8.8.8.8", "1.1.1.1", "10.10.34.36"} {
			if ipIn(ranges, netip.MustParseAddr(ip)) {
				t.Errorf("%s: %s should not be local", c.code, ip)
			}
		}
	}
}

func TestLearner(t *testing.T) {
	for _, c := range []struct {
		code, own, foreign, localIP string
	}{
		{"IR", "digikala.ir", "khanoumi.com", "185.143.232.10"},
		{"CN", "baidu.cn", "example-shop.com", "114.114.114.114"},
	} {
		r, _ := findRegion(c.code)
		dir := t.TempDir()
		b, err := os.ReadFile(r.geoIPFile("rules"))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(r.geoIPFile(dir), b, 0o644)
		if err := installRules(dir); err != nil {
			t.Fatal(err)
		}
		l := newLearner(dir, r)
		if l == nil {
			t.Fatalf("%s: no learner", c.code)
		}
		for host, want := range map[string]bool{c.foreign: true, c.own: false, "1.2.3.4": false, "": false} {
			if got := l.want(host); got != want {
				t.Errorf("%s: want(%q) = %v", c.code, host, got)
			}
		}
		if l.want(c.foreign) {
			t.Errorf("%s: a name was queued twice", c.code)
		}
		if !l.local([]netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr(c.localIP)}) {
			t.Errorf("%s: a local answer was missed", c.code)
		}
		if l.local([]netip.Addr{netip.MustParseAddr("8.8.8.8")}) {
			t.Errorf("%s: a foreign answer counted as local", c.code)
		}
		if err := l.add(c.foreign); err != nil {
			t.Fatal(err)
		}
		if got := readLearned(filepath.Join(dir, r.learnedTag()+".json")); len(got) != 1 || got[0] != c.foreign {
			t.Fatalf("%s: learned list is %v", c.code, got)
		}
		if newLearner(dir, r).want(c.foreign) {
			t.Errorf("%s: a learned name is looked up again after a restart", c.code)
		}
	}
}

func TestRegionOwns(t *testing.T) {
	r, _ := findRegion("IR")
	for host, want := range map[string]bool{
		"digikala.ir": true, "a.b.xn--mgba3a4f16a": true, "aparatcdn.com": true, "static.aparatcdn.com": true,
		"xaparatcdn.com": false, "aparatcdn.com.example.org": false, "example.com": false, "ir": false,
	} {
		if got := r.owns(host); got != want {
			t.Errorf("owns(%q) = %v", host, got)
		}
	}
	if n := r.names(); len(n) != len(r.Suffixes)+len(r.Extra) || &n[0] == &r.Suffixes[0] {
		t.Errorf("names() = %v", n)
	}
	l := &learner{region: r, learned: map[string]bool{}, checked: map[string]time.Time{}}
	if l.want("cdn.aparatcdn.com") {
		t.Error("a listed site is looked up")
	}
}

func TestParseAnswer(t *testing.T) {
	type rec struct {
		t int
		d string
	}
	get := func(r rec) (int, string) { return r.t, r.d }
	a := parseAnswer([]rec{{dnsCNAME, "Edge.Example.IR."}, {dnsA, "185.143.232.10"}, {dnsA, "bad"}, {2, "ns1.host.com."}, {28, "::1"}}, get)
	if len(a.addrs) != 1 || a.addrs[0] != netip.MustParseAddr("185.143.232.10") {
		t.Errorf("addrs = %v", a.addrs)
	}
	if want := []string{"edge.example.ir"}; !slices.Equal(a.names, want) {
		t.Errorf("names = %v, want %v", a.names, want)
	}
}

func TestIsLocal(t *testing.T) {
	r, _ := findRegion("IR")
	dir := t.TempDir()
	b, err := os.ReadFile(r.geoIPFile("rules"))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(r.geoIPFile(dir), b, 0o644)
	if err := installRules(dir); err != nil {
		t.Fatal(err)
	}
	l := newLearner(dir, r)
	type rec struct {
		Type int    `json:"type"`
		Data string `json:"data"`
	}
	// zone answers each name the way a resolver would. Sites whose DNS is
	// hosted in the region but served from abroad stay on the VPN: only
	// where a site is served counts.
	zone := map[string][]rec{
		"shop.com":       {{dnsA, "185.143.232.10"}},
		"alias.com":      {{dnsCNAME, "shop.cdn.ir."}, {dnsA, "104.16.1.1"}},
		"www.alias.com":  {{dnsCNAME, "alias.com."}, {dnsCNAME, "shop.cdn.ir."}, {dnsA, "104.16.1.1"}},
		"dns-in-ir.com":  {{dnsA, "104.16.1.2"}},
		"foreign.com":    {{dnsA, "104.16.1.4"}},
		"cdn-abroad.com": {{dnsCNAME, "cdn-abroad.com.cdn.cloudflare.net."}, {dnsA, "104.16.1.5"}},
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		if q.Get("type") != "A" {
			t.Errorf("asked for %s records", q.Get("type"))
		}
		json.NewEncoder(w).Encode(map[string]any{"Answer": zone[q.Get("name")]})
	}))
	defer srv.Close()
	defer func(s []string) { dohServers = s }(dohServers)
	dohServers = []string{srv.URL}
	for host, want := range map[string]bool{
		"shop.com": true, "alias.com": true, "www.alias.com": true,
		"dns-in-ir.com": false, "foreign.com": false, "cdn-abroad.com": false, "missing.com": false,
	} {
		if got := l.isLocal(context.Background(), srv.Client(), host, r.Subnet); got != want {
			t.Errorf("isLocal(%q) = %v", host, got)
		}
	}
}
