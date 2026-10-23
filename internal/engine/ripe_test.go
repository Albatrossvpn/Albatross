package engine

import (
	"bufio"
	"encoding/binary"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestRIPECoverage checks the bundled Iranian address list against RIPE's
// own record of addresses given to Iran, so a gap shows up in the nightly
// run (RIPE_CHECK=1) rather than as Iranian sites going through the VPN.
func TestRIPECoverage(t *testing.T) {
	if os.Getenv("RIPE_CHECK") == "" {
		t.Skip("set RIPE_CHECK=1")
	}
	resp, err := http.Get("https://ftp.ripe.net/pub/stats/ripencc/delegated-ripencc-latest")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	r, _ := findRegion("IR")
	ranges, err := readIPRuleSet(r.geoIPFile("rules"))
	if err != nil {
		t.Fatal(err)
	}
	var total, covered uint64
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		f := strings.Split(sc.Text(), "|")
		if len(f) < 7 || f[1] != "IR" || f[2] != "ipv4" {
			continue
		}
		start, err := netip.ParseAddr(f[3])
		n, err2 := strconv.ParseUint(f[4], 10, 32)
		if err != nil || err2 != nil {
			continue
		}
		// Sample every /24 of the block.
		base := binary.BigEndian.Uint32(start.AsSlice())
		for off := uint64(0); off < n; off += 256 {
			var b [4]byte
			binary.BigEndian.PutUint32(b[:], base+uint32(off)+1)
			total++
			if ipIn(ranges, netip.AddrFrom4(b)) {
				covered++
			}
		}
	}
	if total == 0 {
		t.Fatal("no Iranian blocks in RIPE's file")
	}
	pct := float64(covered) * 100 / float64(total)
	t.Logf("RIPE IR /24s covered by geoip-ir: %d of %d (%.1f%%)", covered, total, pct)
	if pct < 95 {
		t.Errorf("geoip-ir misses %.1f%% of RIPE's Iranian addresses", 100-pct)
	}
}
