package node

import (
	"encoding/json"
	"strings"
	"testing"
)

// The sidecar protocols parse from their links, run on their helper, and
// survive the round trip through the app's own link form.
func TestSidecarLinks(t *testing.T) {
	cases := []struct {
		link, proto, helper string
		udp                 bool
		want                string // a piece of the helper's command line or files
	}{
		{"naive+https://alice:s%40cret@proxy.example.com:443#N", "naive", "naive", false, `"proxy":"https://alice:s%40cret@proxy.example.com:443"`},
		{"naive://bob:pw@1.2.3.4:8443?proto=quic", "naive", "naive", true, `quic://bob:pw@1.2.3.4:8443`},
		{"juicity://b831381d-6324-4d53-ad4f-8cda48b30811:pw@1.2.3.4:8446?congestion_control=bbr&sni=a.example.com&allow_insecure=0#J", "juicity", "juicity-client", true, `"sni":"a.example.com"`},
		{"mieru://carol:pw@5.6.7.8:2086?protocol=tcp&mtu=1400#M", "mieru", "mieru", false, `"portBindings":[{"port":2086,"protocol":"TCP"}]`},
		{"mieru://carol:pw@example.org:2086?protocol=udp", "mieru", "mieru", true, `"domainName":"example.org"`},
		{"brook://server?server=1.2.3.4%3A9999&password=hello&name=B", "brook", "brook", false, "client --server 1.2.3.4:9999 --password hello --socks5 127.0.0.1:"},
		{"brook://wssserver?wssserver=wss%3A%2F%2Fa.example.com%3A2095%2Fws&password=hello&address=1.2.3.4%3A2095", "brook", "brook", false, "wssclient --wssserver wss://a.example.com:2095/ws --password hello --socks5 127.0.0.1:"},
		{"wstunnel://1.2.3.4:2097?path=s3cret&sni=a.example.com#W", "wstunnel", "wstunnel", false, "--http-upgrade-path-prefix s3cret"},
		{"amneziawg://cPr%2BXL8mZ2rA2lD8Q1D8Q1D8Q1D8Q1D8Q1D8Q1D8Q1E%3D@1.2.3.4:8447?publickey=HIgo9xNzJMWLKASShiTqIybxZ0U3wGLiUeJ1PKf8ykw%3D&address=10.8.0.2%2F32&jc=4&jmin=40&jmax=70&s1=15&s2=18&h1=1234&h2=2345&h3=3456&h4=4567#A", "amneziawg", "wireproxy-awg", true, "Jc = 4\n"},
	}
	for _, c := range cases {
		n, err := ParseLink(c.link)
		if err != nil {
			t.Fatalf("%s: %v", c.link, err)
		}
		if n.Proto != c.proto || n.Sidecar() != c.helper || n.Engine() != EngineSidecar || n.UDPBased() != c.udp {
			t.Fatalf("%s: proto %s helper %s engine %v udp %v", c.link, n.Proto, n.Sidecar(), n.Engine(), n.UDPBased())
		}
		args, env, files := n.SidecarArgs(1080, "/tmp/x")
		all := strings.Join(args, " ") + "\n" + strings.Join(env, " ")
		for _, f := range files {
			all += "\n" + f
		}
		if !strings.Contains(all, c.want) {
			t.Fatalf("%s: helper gets\n%s\nwithout %q", c.link, all, c.want)
		}
		again, err := ParseLink(n.Link())
		if err != nil || again.Key() != n.Key() {
			t.Fatalf("%s: round trip %v, key %s vs %s", c.link, err, again.Key(), n.Key())
		}
		var internal Node
		b, _ := json.Marshal(n)
		if json.Unmarshal(b, &internal) != nil || internal.Key() != n.Key() {
			t.Fatalf("%s: json round trip changes the key", c.link)
		}
	}
}

func TestSidecarLinksRejected(t *testing.T) {
	for _, link := range []string{
		"naive+https://alice@proxy.example.com:443",        // no password
		"juicity://pw@1.2.3.4:8446",                        // no uuid
		"mieru://carol:pw@5.6.7.8:2086?protocol=sctp",      // unknown carrier
		"brook://quicserver?quicserver=quic%3A%2F%2Fa%3A1", // unsupported kind
		"wstunnel://1.2.3.4:2097",                          // no path secret
		"amneziawg://k@1.2.3.4:1?publickey=p&address=10.0.0.2&jc=200&jmin=50&jmax=40",
	} {
		if _, err := ParseLink(link); err == nil {
			t.Errorf("%s parsed", link)
		}
	}
}

// Every value of an AmneziaWG link goes into the helper's INI file: one that
// carries a newline (a section of its own, such as a SOCKS port open to the
// network) or is not of its exact form is refused.
func TestAmneziaWGInjectionRejected(t *testing.T) {
	const priv, pub = "cPr%2BXL8mZ2rA2lD8Q1D8Q1D8Q1D8Q1D8Q1D8Q1D8Q1E%3D", "HIgo9xNzJMWLKASShiTqIybxZ0U3wGLiUeJ1PKf8ykw%3D"
	inject := "%0A%5BSocks5%5D%0ABindAddress%20%3D%200.0.0.0%3A1080"
	base := "amneziawg://" + priv + "@1.2.3.4:8447?publickey=" + pub + "&address=10.8.0.2%2F32&jc=4&jmin=40&jmax=70"
	for _, link := range []string{
		base + "&h1=1" + inject,
		"amneziawg://" + priv + "@1.2.3.4:8447?publickey=" + pub + inject + "&address=10.8.0.2%2F32&jc=4&jmin=40&jmax=70",
		"amneziawg://" + priv + "@1.2.3.4:8447?publickey=" + pub + "&address=10.8.0.2%2F32" + inject + "&jc=4&jmin=40&jmax=70",
		base + "&psk=x" + inject,
		base + "&s1=-1",
		"amneziawg://" + priv + "@1.2.3.4:8447?publickey=short&address=10.8.0.2%2F32&jc=4&jmin=40&jmax=70",
	} {
		if _, err := ParseLink(link); err == nil {
			t.Errorf("%s parsed", link)
		}
	}
}

// Trojan-Go servers are Trojan over WebSocket for the cores.
func TestTrojanGoLink(t *testing.T) {
	n := mustParse(t, "trojan-go://pw@a.example.com:2098?type=ws&host=a.example.com&path=%2Ftg&sni=a.example.com#T")
	if n.Proto != "trojan" || n.Transport != "ws" || n.Path != "/tg" || n.Security != "tls" || n.Engine() != EngineSingBox {
		t.Fatalf("node %+v", n)
	}
}

func TestAmneziaKeyDiffersFromWireGuard(t *testing.T) {
	wg := mustParse(t, "wireguard://cPr%2BXL8mZ2rA2lD8Q1D8Q1D8Q1D8Q1D8Q1D8Q1D8Q1E%3D@1.2.3.4:8447?publickey=HIgo9xNzJMWLKASShiTqIybxZ0U3wGLiUeJ1PKf8ykw%3D&address=10.8.0.2%2F32")
	a := mustParse(t, "amneziawg://cPr%2BXL8mZ2rA2lD8Q1D8Q1D8Q1D8Q1D8Q1D8Q1D8Q1E%3D@1.2.3.4:8447?publickey=HIgo9xNzJMWLKASShiTqIybxZ0U3wGLiUeJ1PKf8ykw%3D&address=10.8.0.2%2F32&jc=4&jmin=40&jmax=70")
	b := mustParse(t, "amneziawg://cPr%2BXL8mZ2rA2lD8Q1D8Q1D8Q1D8Q1D8Q1D8Q1D8Q1E%3D@1.2.3.4:8447?publickey=HIgo9xNzJMWLKASShiTqIybxZ0U3wGLiUeJ1PKf8ykw%3D&address=10.8.0.2%2F32&jc=5&jmin=40&jmax=70")
	if wg.Key() == a.Key() || a.Key() == b.Key() {
		t.Fatal("keys collide")
	}
}

// Keys written without padding, or URL-safe, are taken and written in the
// standard form.
func TestAmneziaWGKeyForms(t *testing.T) {
	n := mustParse(t, "amneziawg://cPr-XL8mZ2rA2lD8Q1D8Q1D8Q1D8Q1D8Q1D8Q1D8Q1E@1.2.3.4:8447?publickey=HIgo9xNzJMWLKASShiTqIybxZ0U3wGLiUeJ1PKf8ykw&address=10.8.0.2%2F32,fd00::2%2F128&jc=4&jmin=40&jmax=70&h1=5-9")
	_, _, files := n.SidecarArgs(1080, "/tmp/x")
	conf := files["wireproxy.conf"]
	for _, want := range []string{"PrivateKey = cPr+XL8mZ2rA2lD8Q1D8Q1D8Q1D8Q1D8Q1D8Q1D8Q1E=\n", "PublicKey = HIgo9xNzJMWLKASShiTqIybxZ0U3wGLiUeJ1PKf8ykw=\n", "Address = 10.8.0.2/32, fd00::2/128\n", "H1 = 5-9\n"} {
		if !strings.Contains(conf, want) {
			t.Fatalf("config lacks %q:\n%s", want, conf)
		}
	}
	if strings.Count(conf, "[") != 3 {
		t.Fatalf("sections:\n%s", conf)
	}
}

// A NaiveProxy link with a TLS name of its own (a relay's) connects to its
// address and says the name: the helper's resolver rules send the name to
// the address.
func TestNaiveWithSNI(t *testing.T) {
	n, err := ParseLink("naive+https://albatross:pw@203.0.113.9:2052?sni=srv.example.com#Naive-relay")
	if err != nil || n.Validate() != nil {
		t.Fatalf("parse %v, validate %v", err, n.Validate())
	}
	_, _, files := n.SidecarArgs(1080, "/tmp/x")
	var cfg map[string]string
	if err := json.Unmarshal([]byte(files["config.json"]), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["proxy"] != "https://albatross:pw@srv.example.com:2052" || cfg["host-resolver-rules"] != "MAP srv.example.com 203.0.113.9" {
		t.Fatalf("config %v", cfg)
	}
	// Without one, as before: the address is the name.
	n, _ = ParseLink("naive+https://albatross:pw@srv.example.com:2052#Naive")
	_, _, files = n.SidecarArgs(1080, "/tmp/x")
	if strings.Contains(files["config.json"], "host-resolver-rules") || !strings.Contains(files["config.json"], "srv.example.com:2052") {
		t.Fatalf("config %s", files["config.json"])
	}
	if n, err := ParseLink("naive+https://albatross:pw@203.0.113.9:2052?sni=a.example%2CMAP%20*%206.6.6.6#x"); err == nil && n.Validate() == nil {
		t.Fatal("a name that adds resolver rules is refused")
	}
}
