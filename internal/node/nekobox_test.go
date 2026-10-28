package node

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"testing"
)

// kryoOut writes what Kryo 5 writes: ints in 4 bytes little-endian,
// strings as ASCII (2 to 32 characters) or with a flagged length.
type kryoOut struct{ bytes.Buffer }

func (k *kryoOut) int(v int) *kryoOut {
	binary.Write(&k.Buffer, binary.LittleEndian, int32(v))
	return k
}

func (k *kryoOut) str(s string) *kryoOut {
	ascii := len(s) > 1 && len(s) <= 32
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
		}
	}
	if ascii {
		k.WriteString(s[:len(s)-1])
		k.WriteByte(s[len(s)-1] | 0x80)
		return k
	}
	n := len([]rune(s)) + 1
	if n < 64 {
		k.WriteByte(0x80 | byte(n))
	} else {
		k.WriteByte(0x80 | 0x40 | byte(n&0x3f))
		k.WriteByte(byte(n >> 6))
	}
	k.WriteString(s) // UTF-8 is Kryo's for every character outside the surrogates
	return k
}

func (k *kryoOut) link(scheme, kind string) string {
	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	w.Write(k.Bytes())
	w.Close()
	return scheme + "://" + kind + "?" + base64.RawURLEncoding.EncodeToString(z.Bytes())
}

// NekoBox's and Exclave's own links: WireGuard in either app's versions,
// SSH with a password, and a custom sing-box config or outbound.
func TestNekoBoxLinks(t *testing.T) {
	wg := func(k *kryoOut) *kryoOut {
		return k.str("162.159.192.1").int(2408).str("172.16.0.2/32\n2606:4700:110:8a36::2/128").str(testWGKey).str(testWARPKey).str("")
	}
	cases := []struct {
		link, proto, name string
		check             func(*Node) bool
	}{
		{wg(new(kryoOut).int(2)).int(1280).str("1,2,3").int(1).str("Germany WARP").str("").str("").link("sn", "wg"), "wireguard", "Germany WARP",
			func(n *Node) bool {
				return n.Port == 2408 && len(n.LocalAddress) == 2 && n.MTU == 1280 && len(n.Reserved) == 3
			}},
		{wg(new(kryoOut).int(4)).int(1280).str("").int(30).int(2).str("Wind1 - Stockholm").int(0).link("exclave", "wg"), "wireguard", "Wind1 - Stockholm",
			func(n *Node) bool { return n.Server == "162.159.192.1" && n.PeerPublicKey == testWARPKey }},
		{new(kryoOut).int(0).str("1.example.uk").int(55627).str("irancpi").int(1).str("freeforiran").str("").int(1).str("cpissh1").str("").str("").link("sn", "ssh"), "ssh", "cpissh1",
			func(n *Node) bool { return n.Port == 55627 && n.Username == "irancpi" && n.Password == "freeforiran" }},
		{new(kryoOut).int(0).str("127.0.0.1").int(1080).int(0).str(`{"outbounds":[{"type":"trojan","tag":"t","server":"t.example.com","server_port":443,"password":"pw","tls":{"enabled":true,"server_name":"t.example.com"}},{"type":"direct","tag":"direct"}]}`).
			int(1).str("Custom").str("").str("").link("sn", "config"), "trojan", "Custom",
			func(n *Node) bool { return n.Server == "t.example.com" && n.Password == "pw" && n.Security == "tls" }},
		{new(kryoOut).int(0).str("127.0.0.1").int(1080).int(1).str(`{"type":"vless","server":"1.2.3.4","server_port":443,"uuid":"`+testUUID+`"}`).
			int(1).str("One").str("").str("").link("sn", "config"), "vless", "One",
			func(n *Node) bool { return n.Server == "1.2.3.4" && n.UUID == testUUID }},
	}
	for _, c := range cases {
		n, err := ParseLink(c.link)
		if err != nil {
			t.Errorf("%s: %v", c.link[:20], err)
			continue
		}
		if n.Proto != c.proto || n.Name != c.name || !c.check(n) {
			t.Errorf("%s: %+v", c.link[:20], n)
		}
	}
	for _, bad := range []string{"sn://wg", "sn://wg?!!", "sn://hysteria2?" + base64.RawURLEncoding.EncodeToString([]byte("x")), new(kryoOut).int(0).str("h").link("sn", "ssh")} {
		if _, err := ParseLink(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}
