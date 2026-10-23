package antiblock

import (
	"net"
	"testing"
)

func TestSampleIPv6(t *testing.T) {
	old := hasIPv6
	hasIPv6 = func() bool { return true }
	defer func() { hasIPv6 = old }()
	v6 := 0
	for _, ip := range sampleIPs(40) {
		p := net.ParseIP(ip)
		if p.To4() == nil {
			v6++
		}
		if !IsCloudflare(ip) {
			t.Fatalf("%s is not a Cloudflare address", ip)
		}
	}
	if v6 != 10 {
		t.Fatalf("%d IPv6 samples, want 10", v6)
	}
}
