package tester

import (
	"testing"

	"github.com/cezomagoanlace29861-jpg/vp/internal/core"
	"github.com/cezomagoanlace29861-jpg/vp/internal/node"
)

// Android denies apps the netlink socket sing-box watches interfaces with,
// and sing-box then refuses to start when auto_detect_interface is on.
func TestMobileTestConfigSkipsInterfaceMonitor(t *testing.T) {
	n, err := node.ParseLink("vless://b831381d-6324-4d53-ad4f-8cda48b30811@example.com:443?security=tls&type=tcp#x")
	if err != nil {
		t.Fatal(err)
	}
	set, err := core.BuildSet([]*node.Node{n}, core.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, mobile := range []bool{false, true} {
		route := testConfig(set, 1080, "p", "", mobile)["route"].(map[string]any)
		if got := route["auto_detect_interface"]; got != !mobile {
			t.Errorf("mobile=%v: auto_detect_interface = %v", mobile, got)
		}
	}
}
