package node

import (
	"encoding/json"
	"strings"
	"testing"
)

const leapEIP = `{"gateways":[{"capabilities":{"transport":[{"ports":["53","1194"],"protocols":["tcp","udp"],"type":"openvpn"},{"options":{"cert":"x"},"ports":["443"],"protocols":["tcp"],"type":"obfs4"}]},"host":"vpn01-sea.riseup.net","ip_address":"204.13.164.252","location":"Seattle"}],"locations":{"Seattle":{"country_code":"US","name":"Seattle"}},"openvpn_configuration":{"auth":"SHA512","cipher":"AES-256-GCM","data-ciphers":"AES-256-GCM","tls-cipher":"x"},"serial":3,"version":3}`

const leapPEM = "-----BEGIN RSA PRIVATE KEY-----\nMIIE\n-----END RSA PRIVATE KEY-----\n-----BEGIN CERTIFICATE-----\nMIID\n-----END CERTIFICATE-----\n"

func TestParseLEAP(t *testing.T) {
	b, _ := json.Marshal(LEAPDoc{Provider: "riseup", EIP: json.RawMessage(leapEIP), Cert: leapPEM, CA: "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"})
	got := ParseLEAP(string(b))
	if len(got) != 4 {
		t.Fatalf("want 4 nodes (2 protocols x 2 ports), got %d", len(got))
	}
	first := got[0]
	if first.Country != "US" || first.Node.Proto != "openvpn" || first.Node.Server != "204.13.164.252" || first.Node.Port != 53 || first.Node.OVPN.Network != "udp" {
		t.Fatalf("first node should be UDP 53 in US: %+v %+v", first.Node, first.Node.OVPN)
	}
	v := first.Node.OVPN
	if v.Cipher != "AES-256-GCM" || v.Auth != "SHA512" || len(v.DataCipher) != 1 {
		t.Fatalf("cipher settings not taken: %+v", v)
	}
	if len(v.CA) != 3 || v.CA[0] != "-----BEGIN CERTIFICATE-----" || len(v.Cert) != 3 || v.Cert[1] != "MIID" || len(v.Key) != 3 || v.Key[1] != "MIIE" {
		t.Fatalf("PEM blocks: ca=%v cert=%v key=%v", v.CA, v.Cert, v.Key)
	}
	if !strings.HasPrefix(first.Node.Name, "Riseup Seattle") || !strings.HasPrefix(first.Node.Source, "leap://riseup/vpn01-sea.riseup.net/") {
		t.Fatalf("name/source: %q %q", first.Node.Name, first.Node.Source)
	}
	cfg, _ := first.Node.SingBox("out", SingBoxOptions{})
	tls, _ := cfg["tls"].(map[string]any)
	if tls == nil || tls["client_certificate"] == nil || tls["client_key"] == nil {
		t.Fatalf("sing-box config lacks the client certificate: %v", cfg)
	}
	if ParseLEAP(`{"provider":"riseup","eip":{},"cert":"","ca":""}`) != nil || ParseLEAP("junk") != nil {
		t.Fatal("incomplete documents must yield nothing")
	}
}
