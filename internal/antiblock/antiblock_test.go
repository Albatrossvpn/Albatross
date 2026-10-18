package antiblock

import (
	"net"
	"testing"

	"github.com/cezomagoanlace29861-jpg/vp/internal/node"
)

func TestPoisoned(t *testing.T) {
	for ip, want := range map[string]bool{"10.10.34.35": true, "127.0.0.1": true, "192.168.1.1": true, "104.16.1.1": false, "1.1.1.1": false} {
		if got := Poisoned(net.ParseIP(ip)); got != want {
			t.Errorf("Poisoned(%s) = %v", ip, got)
		}
	}
}

func TestPinKeepsNames(t *testing.T) {
	n, err := node.ParseLink("vless://0633470c-62e8-4e43-a7b3-bb2f61ff53f7@cdn.example.com:443?type=ws&security=tls&path=%2Fws#x")
	if err != nil {
		t.Fatal(err)
	}
	p := Pin(n, "104.16.5.5")
	if p.Server != "104.16.5.5" || p.SNI != "cdn.example.com" || p.Host != "cdn.example.com" {
		t.Fatalf("pinned = %+v", p)
	}
	if n.Server != "cdn.example.com" {
		t.Fatal("Pin must not change the original")
	}
	if !CDNFrontable(n) {
		t.Fatal("ws+tls on 443 with a name is CDN frontable")
	}
	r, _ := node.ParseLink("vless://0633470c-62e8-4e43-a7b3-bb2f61ff53f7@1.2.3.4:443?security=reality&pbk=ciPKFr9TEbF41ECucqPFlAaHkG54CoZ0CFfHcX3i3iI&sid=6ba85179&sni=www.speedtest.net&type=tcp")
	if CDNFrontable(r) {
		t.Fatal("reality is never CDN frontable")
	}
}

func TestSampleIPsAreCloudflare(t *testing.T) {
	ips := sampleIPs(200)
	if len(ips) < 150 {
		t.Fatalf("only %d samples", len(ips))
	}
	for _, ip := range ips {
		if !IsCloudflare(ip) {
			t.Fatalf("%s is not a Cloudflare IP", ip)
		}
	}
}
