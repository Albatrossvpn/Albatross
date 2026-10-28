package node

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseClash reads the "proxies" list of a Clash / Mihomo profile.
func ParseClash(text string) (out []*Node) {
	defer func() {
		if r := recover(); r != nil {
			out = nil // a parser's bug on one list leaves that list out
		}
	}()
	return parseClash(text)
}

func parseClash(text string) []*Node {
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil
	}
	var out []*Node
	for _, p := range doc.Proxies {
		n := clashProxy(p)
		if n == nil {
			continue
		}
		n.normalize()
		if n.Validate() == nil {
			out = append(out, n)
		}
	}
	return out
}

type cmap map[string]any

func (m cmap) s(keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s := str(v); s != "" {
				return s
			}
		}
	}
	return ""
}

func (m cmap) b(keys ...string) bool {
	for _, k := range keys {
		if truthy(m.s(k)) {
			return true
		}
	}
	return false
}

func (m cmap) m(key string) cmap {
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return cmap{}
}

func (m cmap) list(key string) []string {
	switch v := m[key].(type) {
	case []any:
		var out []string
		for _, x := range v {
			if s := str(x); s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		return splitList(v)
	}
	return nil
}

func clashProxy(raw map[string]any) *Node {
	p := cmap(raw)
	n := &Node{
		Name:   p.s("name"),
		Server: p.s("server"),
		Port:   atoi(p.s("port")),
	}
	tlsOn := p.b("tls")
	n.SNI = p.s("servername", "sni")
	n.Insecure = p.b("skip-cert-verify")
	n.ALPN = p.list("alpn")
	n.Fingerprint = p.s("client-fingerprint")
	if r := p.m("reality-opts"); len(r) > 0 {
		n.Security = "reality"
		n.PublicKey = r.s("public-key")
		n.ShortID = r.s("short-id")
	} else if tlsOn {
		n.Security = "tls"
	}
	switch p.s("type") {
	case "vmess":
		n.Proto = "vmess"
		n.UUID = p.s("uuid")
		n.AlterID = atoi(p.s("alterId"))
		n.Method = p.s("cipher")
		clashTransport(n, p)
	case "vless":
		n.Proto = "vless"
		n.UUID = p.s("uuid")
		n.Flow = p.s("flow")
		n.Encryption = p.s("encryption")
		clashTransport(n, p)
	case "trojan":
		n.Proto = "trojan"
		n.Password = p.s("password")
		if n.Security == "" {
			n.Security = "tls"
		}
		clashTransport(n, p)
	case "ss":
		n.Proto = "shadowsocks"
		n.Method = strings.ToLower(p.s("cipher"))
		n.Password = p.s("password")
		switch p.s("plugin") {
		case "obfs":
			o := p.m("plugin-opts")
			n.Plugin = "obfs-local"
			n.PluginOpts = "obfs=" + firstNonEmpty(o.s("mode"), "http")
			if h := o.s("host"); h != "" {
				n.PluginOpts += ";obfs-host=" + h
			}
		case "shadow-tls":
			o := p.m("plugin-opts")
			n.Plugin = "shadow-tls"
			n.PluginOpts = "host=" + o.s("host") + ";password=" + o.s("password") + ";version=" + firstNonEmpty(o.s("version"), "3")
		case "v2ray-plugin":
			o := p.m("plugin-opts")
			n.Plugin = "v2ray-plugin"
			opts := []string{"mode=" + firstNonEmpty(o.s("mode"), "websocket")}
			if o.b("tls") {
				opts = append(opts, "tls")
			}
			if h := o.s("host"); h != "" {
				opts = append(opts, "host="+h)
			}
			if pa := o.s("path"); pa != "" {
				opts = append(opts, "path="+pa)
			}
			n.PluginOpts = strings.Join(opts, ";")
		case "":
		default:
			return nil
		}
	case "hysteria2":
		n.Proto = "hysteria2"
		n.Security = "tls"
		n.Password = p.s("password", "auth")
		n.Obfs = p.s("obfs")
		n.ObfsPassword = p.s("obfs-password")
		n.UpMbps = atoi(p.s("up"))
		n.DownMbps = atoi(p.s("down"))
		if ports := p.s("ports"); ports != "" {
			n.PortHop = ports
		}
	case "hysteria":
		n.Proto = "hysteria"
		n.Security = "tls"
		if pr := p.s("protocol"); pr != "" && pr != "udp" {
			return nil
		}
		n.Password = p.s("auth-str", "auth_str", "auth")
		n.ObfsPassword = p.s("obfs")
		n.UpMbps = atoi(p.s("up"))
		n.DownMbps = atoi(p.s("down"))
		if ports := p.s("ports"); ports != "" {
			n.PortHop = ports
		}
	case "tuic":
		n.Proto = "tuic"
		n.Security = "tls"
		n.UUID = p.s("uuid")
		n.Password = p.s("password")
		n.CongestionControl = p.s("congestion-controller")
		n.UDPRelayMode = p.s("udp-relay-mode")
		n.DisableSNI = p.b("disable-sni")
	case "anytls":
		n.Proto = "anytls"
		n.Security = "tls"
		n.Password = p.s("password")
	case "socks5":
		n.Proto = "socks"
		n.Username = p.s("username")
		n.Password = p.s("password")
		if tlsOn {
			return nil
		}
	case "http":
		// With tls: true, an HTTPS proxy.
		n.Proto = "http"
		n.Username = p.s("username")
		n.Password = p.s("password")
	case "wireguard":
		n.Proto = "wireguard"
		n.PrivateKey = p.s("private-key")
		n.PeerPublicKey = p.s("public-key")
		n.PreSharedKey = p.s("pre-shared-key")
		n.MTU = atoi(p.s("mtu"))
		if ip := p.s("ip"); ip != "" {
			n.LocalAddress = append(n.LocalAddress, ip)
		}
		if ip := p.s("ipv6"); ip != "" {
			n.LocalAddress = append(n.LocalAddress, ip)
		}
		if r, ok := raw["reserved"].([]any); ok && len(r) == 3 {
			for _, x := range r {
				n.Reserved = append(n.Reserved, atoi(str(x)))
			}
		} else if rs := p.s("reserved"); rs != "" {
			n.Reserved = parseReserved(rs)
		}
		// mihomo's AmneziaWG: WireGuard with its junk packets and headers.
		if a := p.m("amnezia-wg-option"); len(a) > 0 {
			n.Proto = "amneziawg"
			n.AWG = &AmneziaWG{Jc: atoi(a.s("jc")), Jmin: atoi(a.s("jmin")), Jmax: atoi(a.s("jmax")), S1: atoi(a.s("s1")), S2: atoi(a.s("s2")),
				H1: a.s("h1"), H2: a.s("h2"), H3: a.s("h3"), H4: a.s("h4")}
		}
	case "ssh":
		n.Proto = "ssh"
		n.Username = p.s("username")
		n.Password = p.s("password")
		if n.Port == 0 {
			n.Port = 22
		}
		if keys := p.list("host-key"); len(keys) > 0 {
			n.HostKey = keys[0]
		}
	case "ssr":
		n.Proto = "ssr"
		n.Method = p.s("cipher")
		n.Password = p.s("password")
		n.Obfs = p.s("obfs")
		n.SSRProtocol = p.s("protocol")
		n.SSRObfsParam = p.s("obfs-param")
		n.SSRProtocolParam = p.s("protocol-param")
	case "snell":
		n.Proto = "snell"
		n.Password = p.s("psk")
		n.SnellVersion = atoi(p.s("version"))
		o := p.m("obfs-opts")
		n.Obfs, n.Host = o.s("mode"), o.s("host")
	case "mieru":
		n.Proto = "mieru"
		n.Username = p.s("username")
		n.Password = p.s("password")
		n.Transport = strings.ToLower(firstNonEmpty(p.s("transport"), "tcp"))
		if n.Port == 0 {
			// A range of ports instead: its first.
			first, _, _ := strings.Cut(p.s("port-range"), "-")
			n.Port = atoi(strings.TrimSpace(first))
		}
	default:
		return nil
	}
	return n
}

func clashTransport(n *Node, p cmap) {
	n.Transport = p.s("network")
	switch n.Transport {
	case "ws":
		o := p.m("ws-opts")
		n.Path = o.s("path")
		n.Host = o.m("headers").s("Host", "host")
		n.EarlyData = atoi(o.s("max-early-data"))
	case "grpc":
		n.ServiceName = p.m("grpc-opts").s("grpc-service-name")
	case "h2":
		o := p.m("h2-opts")
		n.Host = strings.Join(o.list("host"), ",")
		n.Path = o.s("path")
	case "http":
		o := p.m("http-opts")
		n.Transport = "tcp"
		n.HeaderType = "http"
		if paths := o.list("path"); len(paths) > 0 {
			n.Path = paths[0]
		}
		if h, ok := o.m("headers")["Host"].([]any); ok && len(h) > 0 {
			n.Host = str(h[0])
		}
	case "httpupgrade":
		o := p.m("http-upgrade-opts")
		if len(o) == 0 {
			o = p.m("ws-opts")
		}
		n.Path = o.s("path")
		n.Host = o.s("host")
	case "xhttp":
		o := p.m("xhttp-opts")
		n.Path = o.s("path")
		n.Host = o.s("host")
		n.Mode = o.s("mode")
	}
}
