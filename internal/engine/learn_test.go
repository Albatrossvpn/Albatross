package engine

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"
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
