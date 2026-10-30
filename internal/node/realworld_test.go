package node

import (
	"encoding/base64"
	"strings"
	"testing"
)

// Forms found in the public lists that the parser used to get wrong.
const uid = "b831381d-6324-4d53-ad4f-8cda48b30811"

func TestEscapedLinkInPlainText(t *testing.T) {
	// A link kept with JSON's escapes inside an ordinary list: every
	// parameter after the first was lost and the server ran without TLS.
	text := "first line\nsecond line\n" +
		`vless://` + uid + `@1.2.3.4:443?security=tls\u0026sni=a.example.com\u0026type=ws\u0026path=%2Fws` + "\n"
	links := ExtractLinks(text)
	if len(links) != 1 {
		t.Fatalf("links: %q", links)
	}
	n := mustParse(t, links[0])
	if n.Security != "tls" || n.SNI != "a.example.com" || n.Transport != "ws" || n.Path != "/ws" {
		t.Fatalf("escaped parameters lost: %+v", n)
	}
	// The next link glued on with an escaped line break no longer ends up
	// in this one's last value.
	text = "a\nb\nc\n" + `vless://` + uid + `@1.2.3.4:443?type=ws&path=%2Fws\nvless://` + uid + `@5.6.7.8:443?type=tcp` + "\n"
	links = ExtractLinks(text)
	if len(links) != 2 || mustParse(t, links[0]).Path != "/ws" || mustParse(t, links[1]).Server != "5.6.7.8" {
		t.Fatalf("glued links: %q", links)
	}
}

func TestSchemesTheExtractorMissed(t *testing.T) {
	text := "mierus://u:p@1.2.3.4?port=7000&profile=default&protocol=TCP#M\n" +
		"socks5h://user:pw@1.2.3.4:1080#S\n" +
		"brook://wssserver?wssserver=wss%3A%2F%2Fa.example.com%3A2095%2Fws&password=hello\n" +
		"brook://wssserver?wssserver=wss://a.example.com:2095/ws&password=hello\n"
	links := ExtractLinks(text)
	if len(links) != 4 {
		t.Fatalf("links: %q", links)
	}
	if n := mustParse(t, links[0]); n.Proto != "mieru" {
		t.Fatalf("mierus: %+v", n)
	}
	if n := mustParse(t, links[1]); n.Proto != "socks" || n.Password != "pw" {
		t.Fatalf("socks5h: %+v", n)
	}
	if !strings.Contains(links[3], "password=hello") {
		t.Fatalf("a link inside a query value split the link: %q", links[3])
	}
	// A link stuck to base64 padding still starts a link of its own.
	glued := "vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"add":"1.2.3.4","port":"443","id":"`+uid+`","net":"tcp"}`)) +
		"vless://" + uid + "@5.6.7.8:443?type=tcp"
	if links := ExtractLinks(glued); len(links) != 2 {
		t.Fatalf("glued to padding: %q", links)
	}
}

func TestUUIDWithoutDashes(t *testing.T) {
	n := mustParse(t, "vless://"+strings.ReplaceAll(uid, "-", "")+"@1.2.3.4:443?type=tcp")
	if n.UUID != uid {
		t.Fatalf("a UUID without dashes was remapped: %s", n.UUID)
	}
	if n := mustParse(t, "vless://my-name@1.2.3.4:443?type=tcp"); n.UUID == "my-name" || !isUUID(n.UUID) {
		t.Fatalf("a name is still mapped to a UUID: %s", n.UUID)
	}
}

func TestVMessForms(t *testing.T) {
	enc := func(s string) string { return "vmess://" + base64.StdEncoding.EncodeToString([]byte(s)) }
	n := mustParse(t, enc(`{"add":"1.2.3.4","port":"443","id":"`+uid+`","net":"xhttp","type":"packet-up","path":"/x","tls":"tls","sni":"a.example.com"}`))
	if n.Transport != "xhttp" || n.Mode != "packet-up" || n.HeaderType != "" {
		t.Fatalf("XHTTP mode in type: %+v", n)
	}
	n = mustParse(t, enc(`{"v":"2","ps": [🏁]t.me/ConfigsHub","add":"1.2.3.4","port":"443","id":"`+uid+`","net":"ws","path":"/","tls":""}`))
	if n.Server != "1.2.3.4" || n.Transport != "ws" {
		t.Fatalf("unquoted name: %+v", n)
	}
}

func TestHysteria2DefaultPort(t *testing.T) {
	n := mustParse(t, "hysteria2://pw@h.example.com/?sni=h.example.com#H")
	if n.Port != 443 {
		t.Fatalf("port %d, want the default 443", n.Port)
	}
}

func TestShadowsocksPluginNone(t *testing.T) {
	user := base64.RawURLEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:pw"))
	n := mustParse(t, "ss://"+user+"@1.2.3.4:8388?plugin=none#S")
	if n.Plugin != "" {
		t.Fatalf("plugin %q", n.Plugin)
	}
}

func TestClashHTTPUpgrade(t *testing.T) {
	yaml := `proxies:
  - name: u
    type: vless
    server: 1.2.3.4
    port: 443
    uuid: ` + uid + `
    network: ws
    tls: true
    servername: a.example.com
    ws-opts:
      path: /up
      v2ray-http-upgrade: true
      headers:
        Host: a.example.com
`
	nodes := ParseClash(yaml)
	if len(nodes) != 1 || nodes[0].Transport != "httpupgrade" || nodes[0].Path != "/up" || nodes[0].Host != "a.example.com" {
		t.Fatalf("v2ray-http-upgrade: %+v", nodes)
	}
}

func TestConfigOptionsKept(t *testing.T) {
	sb := `{"outbounds":[{"type":"hysteria","tag":"h","server":"1.2.3.4","server_port":443,"auth_str":"pw","obfs":"secret",
		"up_mbps":10,"down_mbps":50,"tls":{"enabled":true,"server_name":"a.example.com"}}]}`
	var got []*Node
	EachText(sb, func(n *Node) { got = append(got, n) })
	if len(got) != 1 || got[0].ObfsPassword != "secret" {
		t.Fatalf("sing-box hysteria obfs: %+v", got)
	}
	xr := `{"outbounds":[{"protocol":"vless","settings":{"vnext":[{"address":"1.2.3.4","port":443,"users":[{"id":"` + uid + `","encryption":"none"}]}]},
		"streamSettings":{"network":"xhttp","security":"tls","tlsSettings":{"serverName":"a.example.com"},
		"xhttpSettings":{"path":"/x","mode":"auto","xPaddingBytes":"100-1000","downloadSettings":{"address":"5.6.7.8","port":443}}}}]}`
	got = nil
	EachText(xr, func(n *Node) { got = append(got, n) })
	if len(got) != 1 || !strings.Contains(got[0].Extra, "downloadSettings") || !strings.Contains(got[0].Extra, "xPaddingBytes") {
		t.Fatalf("Xray XHTTP options: %+v", got)
	}
}
