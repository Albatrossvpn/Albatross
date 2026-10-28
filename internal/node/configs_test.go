package node

import (
	"encoding/base64"
	"strings"
	"testing"
)

const (
	testWGKey   = "cPr+XL8mZ2rA2lD8Q1D8Q1D8Q1D8Q1D8Q1D8Q1D8Q1E="
	testWARPKey = "bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo="
	testPBK     = "SbVKOEMjK0sIlbwg4akyBg5mL5KZwwB-ed4eEE7YnRc"
	testUUID    = "b831381d-6324-4d53-ad4f-8cda48b30811"
)

func protos(nodes []*Node) map[string]*Node {
	out := map[string]*Node{}
	for _, n := range nodes {
		out[n.Proto] = n
	}
	return out
}

// A sing-box config's proxy outbounds and WireGuard endpoint are servers;
// its selectors, direct and block outbounds are not.
func TestParseConfigsSingBox(t *testing.T) {
	cfg := `{"log":{},"outbounds":[
	 {"type":"selector","tag":"proxy","outbounds":["a","b"]},
	 {"type":"vless","tag":"a","server":"1.2.3.4","server_port":443,"uuid":"` + testUUID + `","flow":"xtls-rprx-vision",
	  "tls":{"enabled":true,"server_name":"www.example.com","utls":{"enabled":true,"fingerprint":"chrome"},"reality":{"enabled":true,"public_key":"` + testPBK + `","short_id":"ab"}}},
	 {"type":"hysteria2","tag":"b","server":"h.example.com","server_port":443,"server_ports":["20000:30000"],"password":"pw","obfs":{"type":"salamander","password":"ob"},"tls":{"enabled":true,"server_name":"h.example.com"}},
	 {"type":"tuic","tag":"c","server":"t.example.com","server_port":8443,"uuid":"` + testUUID + `","password":"pw","congestion_control":"bbr","tls":{"enabled":true,"server_name":"t.example.com"}},
	 {"type":"ssh","tag":"d","server":"5.6.7.8","server_port":22,"user":"u","password":"p"},
	 {"type":"vmess","tag":"e","server":"v.example.com","server_port":443,"uuid":"` + testUUID + `","security":"auto","transport":{"type":"ws","path":"/ws","headers":{"Host":"v.example.com"}},"tls":{"enabled":true,"server_name":"v.example.com"}},
	 {"type":"direct","tag":"direct"},{"type":"block","tag":"block"}],
	 "endpoints":[{"type":"wireguard","tag":"wg","address":["172.16.0.2/32"],"private_key":"` + testWGKey + `",
	  "peers":[{"address":"162.159.192.1","port":2408,"public_key":"` + testWARPKey + `","reserved":[1,2,3]}]}]}`
	var got []*Node
	EachText(cfg, func(n *Node) { got = append(got, n) })
	p := protos(got)
	if len(got) != 6 {
		t.Fatalf("got %d servers: %v", len(got), p)
	}
	if v := p["vless"]; v.Security != "reality" || v.PublicKey != testPBK || v.SNI != "www.example.com" || v.Flow != "xtls-rprx-vision" {
		t.Errorf("vless: %+v", v)
	}
	if h := p["hysteria2"]; h.ObfsPassword != "ob" || h.PortHop != "20000-30000" {
		t.Errorf("hysteria2: %+v", h)
	}
	if w := p["wireguard"]; w.Server != "162.159.192.1" || w.Port != 2408 || len(w.Reserved) != 3 || w.PeerPublicKey != testWARPKey {
		t.Errorf("wireguard: %+v", w)
	}
	if v := p["vmess"]; v.Transport != "ws" || v.Path != "/ws" || v.Host != "v.example.com" {
		t.Errorf("vmess: %+v", v)
	}
	if s := p["ssh"]; s.Username != "u" || s.Password != "p" {
		t.Errorf("ssh: %+v", s)
	}
}

// v2rayNG exports an array of whole Xray configs, each named by its
// remarks; a freedom outbound with a fragment that the proxy dials through
// asks for the TLS hello to be split.
func TestParseConfigsXray(t *testing.T) {
	cfg := `[{"remarks":"🇩🇪 Germany","outbounds":[
	  {"tag":"proxy","protocol":"vless","settings":{"vnext":[{"address":"1.2.3.4","port":443,"users":[{"id":"` + testUUID + `","encryption":"none","flow":""}]}]},
	   "streamSettings":{"network":"ws","security":"tls","tlsSettings":{"serverName":"a.example.com","fingerprint":"chrome"},"wsSettings":{"path":"/p","headers":{"Host":"a.example.com"}},"sockopt":{"dialerProxy":"frag"}}},
	  {"tag":"frag","protocol":"freedom","settings":{"fragment":{"packets":"tlshello","length":"10-20"}}},
	  {"tag":"direct","protocol":"freedom"}]},
	 {"remarks":"NL","outbounds":[
	  {"protocol":"trojan","settings":{"servers":[{"address":"t.example.com","port":443,"password":"pw"}]},
	   "streamSettings":{"network":"grpc","security":"reality","realitySettings":{"serverName":"www.example.org","publicKey":"` + testPBK + `","shortId":"ab"},"grpcSettings":{"serviceName":"svc"}}},
	  {"protocol":"shadowsocks","settings":{"servers":[{"address":"9.9.9.9","port":8388,"method":"chacha20-ietf-poly1305","password":"pw"}]}}]}]`
	var got []*Node
	EachText(cfg, func(n *Node) { got = append(got, n) })
	p := protos(got)
	if len(got) != 3 {
		t.Fatalf("got %d servers: %v", len(got), p)
	}
	if v := p["vless"]; v.Name != "🇩🇪 Germany" || v.Transport != "ws" || v.Host != "a.example.com" || v.SNI != "a.example.com" || !v.Fragment {
		t.Errorf("vless: %+v", v)
	}
	if tr := p["trojan"]; tr.Security != "reality" || tr.ServiceName != "svc" || tr.Transport != "grpc" {
		t.Errorf("trojan: %+v", tr)
	}
	if ss := p["shadowsocks"]; ss.Method != "chacha20-ietf-poly1305" || ss.Server != "9.9.9.9" {
		t.Errorf("shadowsocks: %+v", ss)
	}
}

// WireGuard's INI configs, AmneziaWG's with their junk packet keys, one or
// several in a text, and OpenVPN profiles.
func TestParseConfigsFiles(t *testing.T) {
	wg := "[Interface]\nPrivateKey = " + testWGKey + "\nAddress = 172.16.0.2/32, 2606:4700:110:8a36::2/128\nMTU = 1280\n\n[Peer]\nPublicKey = " + testWARPKey +
		"\nAllowedIPs = 0.0.0.0/0\nEndpoint = engage.cloudflareclient.com:2408\n"
	awg := "[Interface]\nPrivateKey = " + testWGKey + "\nAddress = 10.8.1.2/32\nJc = 4\nJmin = 40\nJmax = 70\nS1 = 15\nS2 = 18\nH1 = 1234\nH2 = 2345\nH3 = 3456\nH4 = 4567\n\n[Peer]\nPublicKey = " + testWARPKey +
		"\nEndpoint = [2001:db8::1]:8447\n"
	var got []*Node
	EachText(wg+"\n"+awg, func(n *Node) { got = append(got, n) })
	p := protos(got)
	if len(got) != 2 || p["wireguard"] == nil || p["amneziawg"] == nil {
		t.Fatalf("got %v", p)
	}
	if w := p["wireguard"]; w.Server != "engage.cloudflareclient.com" || w.Port != 2408 || len(w.LocalAddress) != 2 || w.MTU != 1280 {
		t.Errorf("wireguard: %+v", w)
	}
	if a := p["amneziawg"]; a.Server != "2001:db8::1" || a.AWG.Jc != 4 || a.AWG.S2 != 18 || a.AWG.H4 != "4567" {
		t.Errorf("amneziawg: %+v %+v", a, a.AWG)
	}

	ca := "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"
	profile := "client\ndev tun\nproto tcp\nremote 5.6.7.8 443\ncipher AES-256-GCM\n<ca>\n" + ca + "\n</ca>\n"
	got = nil
	EachText(profile, func(n *Node) { got = append(got, n) })
	if len(got) != 1 || got[0].Proto != "openvpn" || got[0].Server != "5.6.7.8" || got[0].Port != 443 || got[0].OVPN.Network != "tcp" {
		t.Fatalf("openvpn profile: %+v", got)
	}
}

// mihomo's ssh and mieru proxies, and its AmneziaWG (a wireguard proxy with
// amnezia-wg-option).
func TestClashMoreTypes(t *testing.T) {
	yaml := `proxies:
  - {name: s, type: ssh, server: 5.6.7.8, port: 22, username: u, password: p}
  - {name: m, type: mieru, server: 5.6.7.9, port-range: 2090-2099, transport: TCP, username: u, password: p}
  - name: a
    type: wireguard
    server: 162.159.192.1
    port: 2408
    ip: 172.16.0.2
    private-key: ` + testWGKey + `
    public-key: ` + testWARPKey + `
    amnezia-wg-option: {jc: 4, jmin: 40, jmax: 70}
`
	p := protos(ParseClash(yaml))
	if len(p) != 3 {
		t.Fatalf("got %v", p)
	}
	if m := p["mieru"]; m.Port != 2090 || m.Transport != "tcp" {
		t.Errorf("mieru: %+v", m)
	}
	if a := p["amneziawg"]; a.AWG == nil || a.AWG.Jc != 4 || a.Engine() != EngineSidecar {
		t.Errorf("amneziawg: %+v", a)
	}
	if s := p["ssh"]; s.Username != "u" {
		t.Errorf("ssh: %+v", s)
	}
}

// Links that came out of other apps a little broken still parse.
func TestLinksRepaired(t *testing.T) {
	// A key whose plus a URL decoder turned into a space.
	wg := "wireguard://mJ8TH2tdNTGWpKGAH1YMHfd7SiYRWneyDWhCQNCT02g%3D@162.159.192.2:500?address=172.16.0.2%2F32&reserved=8%2C9%2C35&publickey=bmXOC%20F1FxEMF9dyiK2H5%2F1SUtzH0JuVo51h2wPfgyo%3D&mtu=1280#w"
	if n, err := ParseLink(wg); err != nil || n.PeerPublicKey != testWARPKey {
		t.Errorf("wireguard with a spaced key: %v %+v", err, n)
	}
	// Another app's notation in fm: left out, the fragment kept.
	tr := "trojan://humanity@88.216.69.253:443?path=%2Fa&security=tls&fm=tlshello-0-len&host=www.example.org&type=ws&sni=www.example.org#t"
	if n, err := ParseLink(tr); err != nil || !n.Fragment || n.FinalMask != "" {
		t.Errorf("trojan with fm=tlshello: %v %+v", err, n)
	}
	// An "@" in the path.
	hy := "hy2://8d262934-ff34-4ad4-a1fc-933f114f27f3@157.254.158.7:33133/@ELiV2RAY--@ELiV2RAY?sni=akile.nai3.cc#h"
	if n, err := ParseLink(hy); err != nil || n.Server != "157.254.158.7" || n.Port != 33133 || n.Password != "8d262934-ff34-4ad4-a1fc-933f114f27f3" {
		t.Errorf("hy2 with @ in its path: %v %+v", err, n)
	}
	// VMess with a flag, or the next link, stuck to its base64.
	js := `{"add":"1.2.3.4","port":"443","id":"` + testUUID + `","net":"ws","path":"/","tls":"tls","sni":"a.example.com","ps":"v","v":"2"}`
	b64 := base64.StdEncoding.EncodeToString([]byte(js))
	for _, tail := range []string{"🇬🇧", "vless://x@y:1"} {
		if n, err := ParseLink("vmess://" + b64 + tail); err != nil || n.Server != "1.2.3.4" || n.Port != 443 {
			t.Errorf("vmess with %q stuck to it: %v %+v", tail, err, n)
		}
	}
	if n, err := ParseLink("vmess://" + strings.TrimRight(b64, "=") + "🇩🇪🇩🇪"); err != nil || n.UUID != testUUID {
		t.Errorf("unpadded vmess with flags: %v %+v", err, n)
	}
}

// Links side by side with no space between them, in a JSON array or in a
// list kept in a JSON string, all come apart.
func TestExtractLinksStuckTogether(t *testing.T) {
	js := `{"add":"1.2.3.4","port":"443","id":"` + testUUID + `","net":"tcp","ps":"v","v":"2"}`
	vm := "vmess://" + base64.StdEncoding.EncodeToString([]byte(js))
	vl := "vless://" + testUUID + "@5.6.7.8:443?security=tls&sni=a.example.com&type=ws#a"
	tr := "trojan://pw@9.9.9.9:443?sni=b.example.com#b"
	cases := map[string]string{
		"json array":       `["` + vl + `","` + vm + `", "` + tr + `"]`,
		"json string":      `{"sub":"` + strings.ReplaceAll(vl, "&", `&`) + `\n` + vm + `\n` + tr + `"}`,
		"base64 then link": vm + vl + " " + tr,
		"name then link":   tr + "🇩🇪Germany" + vm + "|" + vl,
		"html in json":     `"<br>` + vl + `<br>` + vm + `<br>` + tr + `"`,
	}
	for name, text := range cases {
		got := map[string]bool{}
		EachText(text, func(n *Node) { got[n.Proto] = true })
		if len(got) != 3 {
			t.Errorf("%s: %v from %s", name, got, ExtractLinks(text))
		}
	}
}
