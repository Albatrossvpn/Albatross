package core

import (
	"testing"

	"github.com/Albatrossvpn/vp/internal/node"
)

// Xray's local ports need the login sing-box sends, so no other program on
// the device can use a server through them.
func TestXrayPortsNeedLogin(t *testing.T) {
	n, err := node.ParseLink("vless://b831381d-6324-4d53-ad4f-8cda48b30811@example.com:443?security=tls&type=xhttp&path=%2F#x")
	if err != nil {
		t.Fatal(err)
	}
	if n.Engine() != node.EngineXray {
		t.Fatalf("engine = %v", n.Engine())
	}
	set, err := BuildSet([]*node.Node{n}, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	in := set.Xray["inbounds"].([]any)[0].(map[string]any)["settings"].(map[string]any)
	acct := in["accounts"].([]any)[0].(map[string]any)
	out := set.Outbounds[0].(map[string]any)
	if in["auth"] != "password" || acct["user"] != out["username"] || acct["pass"] != out["password"] || len(acct["pass"].(string)) < 32 {
		t.Fatalf("inbound %v, outbound %v", in, out)
	}
	again, _ := BuildSet([]*node.Node{n}, BuildOptions{})
	if again.Outbounds[0].(map[string]any)["password"] == out["password"] {
		t.Fatal("two sets share a password")
	}
}

// A sidecar node without its helper running gets a discard port, so the
// set still builds and the node simply fails its test.
func TestSidecarOutboundIsSocks(t *testing.T) {
	n, err := node.ParseLink("mieru://carol:pw@5.6.7.8:2086?protocol=tcp#M")
	if err != nil {
		t.Fatal(err)
	}
	set, err := BuildSet([]*node.Node{n}, BuildOptions{warpPorts: map[*node.Node]int{n: 21080}})
	if err != nil {
		t.Fatal(err)
	}
	out := set.Outbounds[0].(map[string]any)
	if out["type"] != "socks" || out["server_port"] != 21080 || out["tag"] != set.Tags[0] {
		t.Fatalf("outbound %v", out)
	}
	if Locate(t.TempDir()).Sidecar("mieru") != "" {
		t.Fatal("found a helper in an empty directory")
	}
}
