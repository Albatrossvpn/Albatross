package node

import (
	"net/url"
	"testing"
)

func TestFinalMaskShapeAndEngine(t *testing.T) {
	base := "vless://b831381d-6324-4d53-ad4f-8cda48b30811@1.2.3.4:443?security=reality&pbk=SbVKOEMjK0sIlbwg4akyBg5mL5KZwwB-ed4eEE7YnRc&sid=ab&type=tcp&fp=chrome&sni=a.com"
	if n, err := ParseLink(base + "&fm=" + url.QueryEscape(`{"tcp":[{"type":"sudoku","settings":{}}]}`)); err != nil || n.Engine() != EngineXray {
		t.Fatalf("a mask only Xray applies must pick Xray: %v %v", err, n)
	}
	if n, err := ParseLink(base + "&fm=" + url.QueryEscape(`{"tcp":[{"type":"fragment"}]}`)); err != nil || n.Engine() != EngineSingBox {
		t.Fatalf("the TLS fragment is sing-box's own: %v %v", err, n)
	}
	if _, err := ParseLink(base + "&fm=" + url.QueryEscape(`{"udp":{"type":"xicmp"}}`)); err == nil {
		t.Fatal("a mask of the wrong shape must be refused, not handed to Xray")
	}
	if _, err := ParseLink("hysteria2://pw@h.example:443?sni=h.example&obfs=salamander"); err == nil {
		t.Fatal("salamander without a password must be refused")
	}
	if _, err := ParseLink("ssh://root:pw@1.2.3.4:22?hk=junk"); err == nil {
		t.Fatal("a host key that is not one must be refused")
	}
}
