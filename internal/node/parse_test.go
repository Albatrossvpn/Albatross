package node

import (
	"encoding/base64"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func mustParse(t *testing.T, link string) *Node {
	t.Helper()
	n, err := ParseLink(link)
	if err != nil {
		t.Fatalf("ParseLink(%q): %v", link, err)
	}
	return n
}

func TestParseVLESSReality(t *testing.T) {
	n := mustParse(t, "vless://0633470c-62e8-4e43-a7b3-bb2f61ff53f7@1.2.3.4:443?security=reality&encryption=none&pbk=ciPKFr9TEbF41ECucqPFlAaHkG54CoZ0CFfHcX3i3iI&sid=6ba85179&fp=firefox&type=tcp&flow=xtls-rprx-vision&sni=www.speedtest.net#%F0%9F%87%A9%F0%9F%87%AA%20DE")
	if n.Proto != "vless" || n.Security != "reality" || n.Flow != "xtls-rprx-vision" || n.Name != "🇩🇪 DE" {
		t.Fatalf("unexpected node: %+v", n)
	}
	if n.Engine() != EngineSingBox {
		t.Fatalf("engine = %v", n.Engine())
	}
	out, ep := n.SingBox("p", SingBoxOptions{})
	if ep {
		t.Fatal("vless is not an endpoint")
	}
	tls := out["tls"].(map[string]any)
	if tls["server_name"] != "www.speedtest.net" || tls["reality"].(map[string]any)["short_id"] != "6ba85179" {
		t.Fatalf("tls = %v", tls)
	}
}

func TestParseVLESSWebSocketEarlyData(t *testing.T) {
	n := mustParse(t, "vless://e6df2095-a73c-4043-8693-8d5a03686870@104.21.91.244:443?path=%2Fws%3Fed%3D2048&amp;security=tls&amp;encryption=none&amp;host=a.example.com&amp;type=ws&amp;alpn=h2,http/1.1#x")
	if n.Path != "/ws" || n.EarlyData != 2048 || n.Host != "a.example.com" {
		t.Fatalf("unexpected node: %+v", n)
	}
	out, _ := n.SingBox("p", SingBoxOptions{})
	tr := out["transport"].(map[string]any)
	if tr["max_early_data"] != 2048 || tr["headers"].(map[string]any)["Host"] != "a.example.com" {
		t.Fatalf("transport = %v", tr)
	}
	if alpn := out["tls"].(map[string]any)["alpn"].([]string); len(alpn) != 1 || alpn[0] != "http/1.1" {
		t.Fatalf("ws must not offer h2: %v", alpn)
	}
	if out["tls"].(map[string]any)["server_name"] != "a.example.com" {
		t.Fatal("SNI should fall back to the Host header")
	}
}

func TestParseVMessJSON(t *testing.T) {
	j, _ := json.Marshal(map[string]any{"v": "2", "ps": "test", "add": "example.com", "port": 8080, "id": "b831381d-6324-4d53-ad4f-8cda48b30811", "aid": "0", "net": "ws", "type": "none", "host": "h.example.com", "path": "/p", "tls": ""})
	n := mustParse(t, "vmess://"+base64.StdEncoding.EncodeToString(j))
	if n.Proto != "vmess" || n.Port != 8080 || n.Transport != "ws" || n.Method != "auto" {
		t.Fatalf("unexpected node: %+v", n)
	}
	// Junk glued after the padding must not break decoding.
	if _, err := ParseLink("vmess://" + base64.StdEncoding.EncodeToString(j) + "vless"); err != nil {
		t.Fatal(err)
	}
}

func TestParseVMessShadowrocket(t *testing.T) {
	enc := base64.RawURLEncoding.EncodeToString([]byte("auto:35d25899-ed20-46d9-9c7c-99c0d414d5e4@static.example.com:80"))
	n := mustParse(t, "vmess://"+enc+"?remarks=static&path=/user&obfs=websocket&alterId=0")
	if n.Server != "static.example.com" || n.Transport != "ws" || n.Path != "/user" || n.Name != "static" {
		t.Fatalf("unexpected node: %+v", n)
	}
}

func TestParseShadowsocksForms(t *testing.T) {
	sip002 := mustParse(t, "ss://YWVzLTI1Ni1nY206WktFc1hrRmJVWjJ6NjY3WUE4QTdjRURQ@212.102.44.87:48970#name")
	if sip002.Method != "aes-256-gcm" || sip002.Password != "ZKEsXkFbUZ2z667YA8A7cEDP" {
		t.Fatalf("sip002: %+v", sip002)
	}
	legacy := mustParse(t, "ss://"+base64.StdEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:pass@1.2.3.4:8388"))+"#old")
	if legacy.Server != "1.2.3.4" || legacy.Port != 8388 || legacy.Name != "old" {
		t.Fatalf("legacy: %+v", legacy)
	}
	plain := mustParse(t, "ss://2022-blake3-aes-128-gcm:"+strings.ReplaceAll(base64.StdEncoding.EncodeToString(make([]byte, 16)), "=", "%3D")+"@1.2.3.4:443")
	if plain.Method != "2022-blake3-aes-128-gcm" {
		t.Fatalf("plain userinfo: %+v", plain)
	}
	obfs := mustParse(t, "ss://YWVzLTEyOC1nY206c2hhZG93c29ja3M@1.2.3.4:443/?plugin=simple-obfs%3Bobfs%3Dhttp%3Bobfs-host%3Dwww.bing.com#o")
	if obfs.Plugin != "obfs-local" || obfs.PluginOpts != "obfs=http;obfs-host=www.bing.com" {
		t.Fatalf("plugin: %+v", obfs)
	}
	// A vless link mislabelled as ss.
	mis := mustParse(t, "ss://4bab9727-7075-4260-a30b-369b3e54ee14@helixgaming.ir:80?path=/&security=none&encryption=none&host=play.google.com&type=ws#x")
	if mis.Proto != "vless" || mis.Transport != "ws" {
		t.Fatalf("mislabelled: %+v", mis)
	}
}

func TestParseHysteria2PortHop(t *testing.T) {
	n := mustParse(t, "hy2://pass@example.com:443?mport=20000-30000&obfs=salamander&obfs-password=x&insecure=1&sni=sni.example.com#h")
	out, _ := n.SingBox("p", SingBoxOptions{})
	if _, ok := out["server_port"]; ok {
		t.Fatal("server_port conflicts with server_ports")
	}
	if p := out["server_ports"].([]string); len(p) != 1 || p[0] != "20000:30000" {
		t.Fatalf("server_ports = %v", p)
	}
	if out["obfs"].(map[string]any)["password"] != "x" {
		t.Fatal("missing obfs")
	}
}

func TestParseWireGuardWARP(t *testing.T) {
	n := mustParse(t, "wireguard://KNOyCvJdN4OrgWjcGyyuGcF%2B3zNBuZKn2xGnmPhViEM%3D@8.6.112.253:1843?address=172.16.0.2%2F32&reserved=0%2C0%2C0&publickey=bmXOC%2BF1FxEMF9dyiK2H5%2F1SUtzH0JuVo51h2wPfgyo%3D&mtu=1280#warp")
	out, ep := n.SingBox("w", SingBoxOptions{})
	if !ep || out["type"] != "wireguard" {
		t.Fatalf("wireguard should be an endpoint: %v", out)
	}
}

func TestPlainHTTPHeaderUsesSingBox(t *testing.T) {
	n := mustParse(t, "vless://007a5c24-cdbe-4adc-a79f-ca2768fef8bb@edge.example.ir:10007?security=none&encryption=none&headerType=http&host=play.google.com&type=tcp")
	if n.Engine() != EngineSingBox {
		t.Fatalf("engine = %v", n.Engine())
	}
	out, _ := n.SingBox("p", SingBoxOptions{})
	tr := out["transport"].(map[string]any)
	if tr["type"] != "http" || tr["method"] != "GET" {
		t.Fatalf("transport = %v", tr)
	}
}

func TestXHTTPPlaintextUsesRedirect(t *testing.T) {
	n := mustParse(t, "vless://0aec6598-7fb5-44e6-bff7-a4d550117830@104.234.135.88:443?mode=auto&path=/&security=none&encryption=none&type=xhttp&host=a.example.com")
	if n.Engine() != EngineXray {
		t.Fatalf("engine = %v", n.Engine())
	}
	outs := n.Xray("x1")
	if len(outs) != 2 || outs[0]["settings"].(map[string]any)["address"] != plaintextAddr || outs[1]["protocol"] != "freedom" {
		t.Fatalf("outbounds = %v", outs)
	}
}

func TestExtractLinksFromHTMLAndBase64(t *testing.T) {
	html := `<div class="tgme_widget_message_text">vless://0633470c-62e8-4e43-a7b3-bb2f61ff53f7@1.2.3.4:443?security=tls&amp;type=ws&amp;host=x.com#a<br/>trojan://p@5.6.7.8:443?sni=y.com#b</div>`
	links := ExtractLinks(html)
	if len(links) != 2 || !strings.Contains(links[0], "&type=ws") {
		t.Fatalf("html links = %q", links)
	}
	sub := base64.StdEncoding.EncodeToString([]byte("trojan://p@5.6.7.8:443#b\nss://YWVzLTEyOC1nY206c2hhZG93c29ja3M@1.2.3.4:443#c\n"))
	if got := ExtractLinks(sub); len(got) != 2 {
		t.Fatalf("base64 links = %q", got)
	}
	spaced := "vless://0633470c-62e8-4e43-a7b3-bb2f61ff53f7@1.2.3.4:443?type=xhttp&extra={  \"a\" : 1}&security=tls#name with spaces"
	if got := ExtractLinks(spaced); len(got) != 1 || !strings.HasSuffix(got[0], "spaces") {
		t.Fatalf("line with spaces = %q", got)
	}
}

func TestParseClash(t *testing.T) {
	yaml := `proxies:
  - {name: a, type: ss, server: 1.2.3.4, port: 443, cipher: aes-128-gcm, password: x}
  - name: b
    type: vless
    server: example.com
    port: 443
    uuid: 0633470c-62e8-4e43-a7b3-bb2f61ff53f7
    tls: true
    network: ws
    ws-opts: {path: /p, headers: {Host: h.example.com}}
  - {name: c, type: hysteria2, server: 5.6.7.8, port: 443, password: pw, sni: s.example.com}
`
	nodes, _ := ParseText(yaml)
	if len(nodes) != 3 || nodes[1].Host != "h.example.com" || nodes[1].Security != "tls" {
		t.Fatalf("nodes = %+v", nodes)
	}
}

func TestKeyIgnoresName(t *testing.T) {
	a := mustParse(t, "trojan://p@5.6.7.8:443?sni=y.com#one")
	b := mustParse(t, "trojan://p@5.6.7.8:443?sni=y.com#two")
	if a.Key() != b.Key() {
		t.Fatal("key should ignore the name")
	}
}

func TestRejectsJunk(t *testing.T) {
	for _, l := range []string{
		"vless://84e71dfb-1fd8-417c-b2da-e42777b8a7a0@127.0.0.1:1234?security=none&type=tcp",
		"ss://bnVsbDpudWxs@104.16.188.2:8880",
		"trojan://x@84.234.31.249:443?security=reality&type=tcp",
		"vless://1My-chanel-Telegram-ATOVPN",
		"ssr://abc",
	} {
		if _, err := ParseLink(l); err == nil {
			t.Errorf("expected %q to be rejected", l)
		}
	}
}

func TestNamesHaveNoPersian(t *testing.T) {
	for in, want := range map[string]string{
		"سرور آلمان 🇩🇪 Germany-1": "🇩🇪 Germany-1",
		"🇺🇸 USA | کانال ما":       "🇺🇸 USA",
		"Plain-Name":              "Plain-Name",
	} {
		if got := cleanName(in); got != want {
			t.Errorf("cleanName(%q) = %q, want %q", in, got, want)
		}
	}
	n, err := ParseLink("trojan://pw@203.0.113.5:443?security=tls&sni=a.example#%D8%B3%D8%B1%D9%88%D8%B1")
	if err != nil {
		t.Fatal(err)
	}
	if n.Name != "TROJAN 203.0.113.5" {
		t.Fatalf("a Persian-only name became %q", n.Name)
	}
}

func TestHysteria2AsksForLineSpeed(t *testing.T) {
	n := mustParse(t, "hy2://pass@example.com:443?sni=example.com#h")
	out, _ := n.SingBox("x", SingBoxOptions{LineMbps: 40})
	if out["down_mbps"] != 50 || out["up_mbps"] != 10 {
		t.Fatalf("up %v down %v", out["up_mbps"], out["down_mbps"])
	}
	if out, _ := n.SingBox("x", SingBoxOptions{}); out["down_mbps"] != nil {
		t.Fatal("asked for a speed nobody measured")
	}
}

func TestParseOutlineJSON(t *testing.T) {
	text := `{"server":"1.2.3.4","server_port":8388,"password":"pw","method":"chacha20-ietf-poly1305","prefix":"\u0016\u0003\u0001"}`
	nodes, seen := ParseText(text)
	if seen != 1 || len(nodes) != 1 {
		t.Fatalf("want one node, got %d (%d seen)", len(nodes), seen)
	}
	n := nodes[0]
	if n.Proto != "shadowsocks" || n.Server != "1.2.3.4" || n.Port != 8388 || n.Method != "chacha20-ietf-poly1305" || n.Password != "pw" {
		t.Fatalf("bad node: %+v", n)
	}
	for _, bad := range []string{`{"server":"1.2.3.4"}`, `{"a":1}`, `[]`, `ss://x`} {
		if ParseOutlineJSON(bad) != nil {
			t.Fatalf("%q should not parse", bad)
		}
	}
}

func TestParseSSH(t *testing.T) {
	n := mustParse(t, "ssh://alice:s3cret@ssh.example.net:2222?hk=ssh-ed25519%20AAAAC3#Free%20SSH")
	if n.Proto != "ssh" || n.Username != "alice" || n.Password != "s3cret" || n.Server != "ssh.example.net" || n.Port != 2222 || n.HostKey != "ssh-ed25519 AAAAC3" || n.Name != "Free SSH" {
		t.Fatalf("bad node: %+v", n)
	}
	if n.Engine() != EngineSingBox {
		t.Fatalf("ssh runs on sing-box, got %v", n.Engine())
	}
	out, _ := n.SingBox("out", SingBoxOptions{})
	if out["type"] != "ssh" || out["user"] != "alice" || out["password"] != "s3cret" || out["server_port"] != 2222 {
		t.Fatalf("bad outbound: %v", out)
	}
	if hk, _ := out["host_key"].([]string); len(hk) != 1 {
		t.Fatalf("host key missing: %v", out)
	}
	d := mustParse(t, "ssh://bob:pw@1.2.3.4")
	if d.Port != 22 {
		t.Fatalf("default port 22, got %d", d.Port)
	}
	if _, err := ParseLink("ssh://nouser@1.2.3.4:22"); err == nil {
		t.Fatal("a link without a password must not parse")
	}
	if _, err := ParseLink("ssh://1.2.3.4:22"); err == nil {
		t.Fatal("a link without a user must not parse")
	}
}

func TestVMessICMPCarrier(t *testing.T) {
	j := `{"v":"2","ps":"Albatross-ICMP","add":"203.0.113.9","port":"7","id":"007a5c24-cdbe-4adc-a79f-ca2768fef8bb","aid":"0","scy":"aes-128-gcm","net":"kcp","type":"none","path":"abcd1234","host":"","tls":"","fm":{"udp":[{"type":"xicmp"}]}}`
	n := mustParse(t, "vmess://"+base64.StdEncoding.EncodeToString([]byte(j)))
	if n.Transport != "kcp" || n.Seed != "abcd1234" || n.FinalMask == "" {
		t.Fatalf("kcp seed or finalmask lost: %+v", n)
	}
	if n.Engine() != EngineXray {
		t.Fatalf("an mKCP node runs on Xray, got %v", n.Engine())
	}
	s := n.xrayStream()
	k, _ := s["kcpSettings"].(map[string]any)
	if k == nil || k["seed"] != "abcd1234" {
		t.Fatalf("kcp settings: %v", s)
	}
	fm, _ := s["finalmask"].(map[string]any)
	udp, _ := fm["udp"].([]any)
	if len(udp) != 1 {
		t.Fatalf("finalmask: %v", s)
	}
	mask := udp[0].(map[string]any)
	settings, _ := mask["settings"].(map[string]any)
	if mask["type"] != "xicmp" || settings == nil || settings["dgram"] != (runtime.GOOS != "windows") {
		t.Fatalf("xicmp mask: %v", mask)
	}
}

func TestShadowTLS(t *testing.T) {
	key := base64.RawURLEncoding.EncodeToString([]byte("2022-blake3-aes-128-gcm:" + base64.StdEncoding.EncodeToString(make([]byte, 16))))
	n := mustParse(t, "ss://"+key+"@203.0.113.9:8445?plugin=shadow-tls%3Bhost%3Dwww.microsoft.com%3Bpassword%3Dpw1%3Bversion%3D3#STLS")
	if n.Plugin != "shadow-tls" || n.pluginOpt("host") != "www.microsoft.com" || n.pluginOpt("password") != "pw1" {
		t.Fatalf("plugin not kept: %+v", n)
	}
	if n.Engine() != EngineSingBox {
		t.Fatalf("shadow-tls runs on sing-box, got %v", n.Engine())
	}
	out, _ := n.SingBox("n1", SingBoxOptions{})
	if out["detour"] != "n1-stls" || out["plugin"] != nil {
		t.Fatalf("the Shadowsocks outbound must detour, not use a plugin: %v", out)
	}
	h := n.SingBoxHelpers("n1")
	if len(h) != 1 || h[0]["type"] != "shadowtls" || h[0]["tag"] != "n1-stls" || h[0]["version"] != 3 || h[0]["password"] != "pw1" {
		t.Fatalf("helper: %v", h)
	}
	tls := h[0]["tls"].(map[string]any)
	if tls["server_name"] != "www.microsoft.com" {
		t.Fatalf("helper tls: %v", tls)
	}
	plain := mustParse(t, "ss://"+key+"@203.0.113.9:8388#SS")
	if plain.SingBoxHelpers("n2") != nil {
		t.Fatal("plain Shadowsocks needs no helper")
	}
	if _, err := ParseLink("ss://" + key + "@203.0.113.9:8445?plugin=shadow-tls%3Bhost%3Dx"); err == nil {
		if nn, _ := ParseLink("ss://" + key + "@203.0.113.9:8445?plugin=shadow-tls%3Bhost%3Dx"); nn.Engine() != EngineNone {
			t.Fatal("shadow-tls without a password must not run")
		}
	}
}

func TestUsqueMethods(t *testing.T) {
	m := WarpPlus("masque", "")
	m2 := WarpPlus("masque2", "")
	if !m.Usque() || !m2.Usque() || WarpPlus("warp", "").Usque() {
		t.Fatal("Usque() must name only the MASQUE methods")
	}
	if m.Engine() != EngineWarpPlus || m2.Engine() != EngineWarpPlus {
		t.Fatalf("MASQUE methods run as sidecar methods: %v %v", m.Engine(), m2.Engine())
	}
	if m.Key() == m2.Key() || m.Name == m2.Name {
		t.Fatal("the two MASQUE methods must differ")
	}
	a := m2.UsqueArgs("127.0.0.1", "1080", "/tmp/x")
	got := strings.Join(a, " ")
	if !strings.Contains(got, "-c /tmp/x/config.json socks -b 127.0.0.1 -p 1080") || !strings.Contains(got, "--http2") {
		t.Fatalf("masque2 args: %v", a)
	}
	if strings.Contains(strings.Join(m.UsqueArgs("127.0.0.1", "1080", "/tmp/x"), " "), "--http2") {
		t.Fatal("masque (HTTP/3) must not ask for HTTP/2")
	}
	r := strings.Join(UsqueRegisterArgs("/tmp/x"), " ")
	if !strings.Contains(r, "register -a") || !strings.Contains(r, "-c /tmp/x/config.json") {
		t.Fatalf("register args: %q", r)
	}
}

func TestVMessDNSTunnel(t *testing.T) {
	j := `{"v":"2","ps":"Albatross-DNS","add":"203.0.113.9","port":"53","id":"007a5c24-cdbe-4adc-a79f-ca2768fef8bb","aid":"0","scy":"aes-128-gcm","net":"kcp","type":"none","path":"abcd1234","host":"","tls":"","fm":{"udp":[{"type":"xdns","settings":{"domains":[{"names":["t.example.com"]}]}}]}}`
	n := mustParse(t, "vmess://"+base64.StdEncoding.EncodeToString([]byte(j)))
	if !n.DNSTunnel() || !StyleDNSTunnel(n.Style()) || !StyleDNSPort(n.Style()) {
		t.Fatalf("a DNS tunnel must be recognised: style %d", n.Style())
	}
	if StyleDNSTunnel(mustParse(t, "vmess://"+base64.StdEncoding.EncodeToString([]byte(`{"v":"2","add":"1.2.3.4","port":"53","id":"007a5c24-cdbe-4adc-a79f-ca2768fef8bb","net":"kcp","path":"s"}`))).Style()) {
		t.Fatal("plain mKCP on 53 is not a DNS tunnel")
	}
	obs := n.Xray("x0")
	if len(obs) == 0 {
		t.Fatal("no outbound")
	}
	ob := obs[0]
	SetDNSTunnelResolvers(ob, "192.168.1.1")
	stream := ob["streamSettings"].(map[string]any)
	k := stream["kcpSettings"].(map[string]any)
	if k["mtu"] != 130 || k["tti"] != 200 {
		t.Fatalf("kcp tuning: %v", k)
	}
	udp := stream["finalmask"].(map[string]any)["udp"].([]any)
	settings := udp[0].(map[string]any)["settings"].(map[string]any)
	res := settings["resolvers"].([]any)[0].(map[string]any)["addrs"].([]string)
	if res[0] != "udp://192.168.1.1:53" || len(res) < 2 {
		t.Fatalf("resolvers: %v", res)
	}
	if n.Engine() != EngineXray {
		t.Fatalf("engine %v", n.Engine())
	}
}

func TestDirectMethod(t *testing.T) {
	n := Direct()
	if n.Engine() != EngineSingBox {
		t.Fatalf("direct runs on sing-box, got %v", n.Engine())
	}
	out, ep := n.SingBox("d", SingBoxOptions{})
	// sing-box refuses fragment fields on a direct outbound; they are
	// route options (core.FragmentRules).
	if _, bad := out["tls_fragment"]; ep || out["type"] != "direct" || bad || out["tls_record_fragment"] != nil {
		t.Fatalf("bad outbound: %v", out)
	}
	back, err := ParseLink(n.Link())
	if err != nil || back.Proto != "direct" || back.Key() != n.Key() {
		t.Fatalf("round trip: %v %+v", err, back)
	}
}

// 3x-ui writes genuine Shadowsocks links with type=tcp: they stay
// Shadowsocks; a userinfo that is no Shadowsocks credential is Trojan.
func TestSSWithTypeStaysShadowsocks(t *testing.T) {
	n, err := ParseLink("ss://Y2hhY2hhMjAtaWV0Zi1wb2x5MTMwNTpTZWM0ZXRQYXNz@1.2.3.4:443?type=tcp#name")
	if err != nil || n.Proto != "shadowsocks" || n.Method != "chacha20-ietf-poly1305" || n.Password != "Sec4etPass" {
		t.Fatalf("3x-ui link: %v %+v", err, n)
	}
	n, err = ParseLink("ss://2022-blake3-aes-128-gcm:YWJjZGVmZ2hpamtsbW5vcA%3D%3D@1.2.3.4:8443?type=tcp&security=none#m")
	if err != nil || n.Proto != "shadowsocks" {
		t.Fatalf("plain 2022 link: %v %+v", err, n)
	}
	n, err = ParseLink("ss://secretpass@example.com:443?type=ws&security=tls&sni=example.com#t")
	if err != nil || n.Proto != "trojan" || n.Password != "secretpass" {
		t.Fatalf("mislabelled trojan: %v %+v", err, n)
	}
}
