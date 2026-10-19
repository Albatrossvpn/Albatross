package engine

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

func TestIranRanges(t *testing.T) {
	ranges, err := readIPRuleSet("rules/geoip-ir.srs")
	if err != nil {
		t.Fatal(err)
	}
	if len(ranges) < 1000 {
		t.Fatalf("only %d ranges", len(ranges))
	}
	for ip, want := range map[string]bool{
		"185.143.232.10": true,  // ArvanCloud, Tehran
		"2.176.0.1":      true,  // TCI
		"8.8.8.8":        false, // Google
		"1.1.1.1":        false,
		"10.10.34.36":    false, // the block page
	} {
		if got := ipIn(ranges, netip.MustParseAddr(ip)); got != want {
			t.Errorf("%s in Iran = %v, want %v", ip, got, want)
		}
	}
}

func TestLearner(t *testing.T) {
	dir := t.TempDir()
	b, _ := os.ReadFile("rules/geoip-ir.srs")
	os.WriteFile(filepath.Join(dir, "geoip-ir.srs"), b, 0o644)
	if err := installRules(dir); err != nil {
		t.Fatal(err)
	}
	l := newLearner(dir)
	if l == nil {
		t.Fatal("no learner")
	}
	for host, want := range map[string]bool{
		"khanoumi.com": true, "digikala.ir": false, "1.2.3.4": false, "": false,
	} {
		if got := l.want(host); got != want {
			t.Errorf("want(%q) = %v", host, got)
		}
	}
	if l.want("khanoumi.com") {
		t.Error("a name was queued twice")
	}
	if !l.iranian([]netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("185.143.232.10")}) {
		t.Error("an Iranian answer was missed")
	}
	if err := l.add("khanoumi.com"); err != nil {
		t.Fatal(err)
	}
	if got := readLearned(filepath.Join(dir, learnedFile)); len(got) != 1 || got[0] != "khanoumi.com" {
		t.Fatalf("learned list is %v", got)
	}
	if newLearner(dir).want("khanoumi.com") {
		t.Error("a learned name is looked up again after a restart")
	}
}
