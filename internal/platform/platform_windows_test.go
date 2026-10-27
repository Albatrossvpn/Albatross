//go:build windows

package platform

import (
	"net"
	"os"
	"testing"
)

func TestSystemDNS(t *testing.T) {
	a := SystemDNS()
	if a == "" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatal("no resolver found on a runner that has a network")
		}
		t.Skip("no network here")
	}
	if ip := net.ParseIP(a); ip == nil || ip.To4() == nil {
		t.Fatalf("SystemDNS = %q, want an IPv4 address", a)
	}
}
