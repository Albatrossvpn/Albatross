package node

import (
	"encoding/json"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// Lists also come as whole client configs rather than share links:
// sing-box's and Xray's JSON (one config, or an array of them, as v2rayNG
// exports them), WireGuard's and AmneziaWG's INI files, and OpenVPN
// profiles. ParseConfigs reads the servers out of any of them.

// ParseConfigs returns the servers of a list written as client configs,
// or nil when text is none of them.
func ParseConfigs(text string) []*Node {
	t := strings.TrimSpace(strings.TrimPrefix(text, "\ufeff"))
	var out []*Node
	add := func(n *Node) {
		if n == nil {
			return
		}
		n.normalize()
		if n.Validate() == nil {
			out = append(out, n)
		}
	}
	switch {
	case (strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")) && strings.Contains(head(t, 1<<20), `"outbounds"`):
		dec := json.NewDecoder(strings.NewReader(t))
		for dec.More() {
			var doc any
			if dec.Decode(&doc) != nil {
				break
			}
			eachOutbound(doc, func(o map[string]any, all []any, name string) {
				var n *Node
				if _, ok := o["type"]; ok {
					n = singBoxOutbound(cmap(o))
				} else {
					n = xrayOutbound(cmap(o), all)
				}
				if n != nil && name != "" {
					n.Name = name
				}
				add(n)
			})
		}
	case strings.Contains(t, "[Interface]") && strings.Contains(t, "[Peer]"):
		for _, n := range ParseWGConf(t) {
			add(n)
		}
	case strings.Contains(t, "<ca>") && ovpnProfile.MatchString(head(t, 1<<16)):
		if n, err := ParseOVPN(t, ""); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// ovpnProfile finds an OpenVPN client profile's remote line.
var ovpnProfile = regexp.MustCompile(`(?m)^\s*remote\s+\S+(\s+\d+)?`)

// eachOutbound calls fn for every outbound in a config (or an array of
// configs), with the config's outbounds, for the ones that chain, and its
// name (v2rayNG's remarks).
func eachOutbound(doc any, fn func(o map[string]any, all []any, name string)) {
	switch v := doc.(type) {
	case []any:
		for _, x := range v {
			eachOutbound(x, fn)
		}
	case map[string]any:
		name := cmap(v).s("remarks")
		for _, key := range []string{"outbounds", "endpoints"} {
			if list, ok := v[key].([]any); ok {
				for _, x := range list {
					if o, ok := x.(map[string]any); ok {
						fn(o, list, name)
					}
				}
			}
		}
	}
}

// num reads a JSON number or string as an int.
func num(v any) int { return atoi(str(v)) }

// singBoxOutbound reads one sing-box outbound (or WireGuard endpoint).
func singBoxOutbound(o cmap) *Node {
	n := &Node{Name: o.s("tag"), Server: o.s("server"), Port: num(o["server_port"])}
	if t := o.m("tls"); t.b("enabled") {
		n.Security = "tls"
		n.SNI = t.s("server_name")
		n.Insecure = t.b("insecure")
		n.ALPN = t.list("alpn")
		n.Fingerprint = t.m("utls").s("fingerprint")
		if r := t.m("reality"); r.b("enabled") {
			n.Security = "reality"
			n.PublicKey = r.s("public_key")
			n.ShortID = r.s("short_id")
		}
	}
	if tr := o.m("transport"); len(tr) > 0 {
		n.Transport = tr.s("type")
		n.Path = tr.s("path")
		n.ServiceName = tr.s("service_name")
		n.Host = firstNonEmpty(tr.m("headers").s("Host", "host"), tr.s("host"))
		if hosts := tr.list("host"); n.Host == "" && len(hosts) > 0 {
			n.Host = hosts[0]
		}
		if n.Transport == "http" {
			n.Transport = "h2"
		}
	}
	switch o.s("type") {
	case "vless":
		n.Proto, n.UUID, n.Flow = "vless", o.s("uuid"), o.s("flow")
	case "vmess":
		n.Proto, n.UUID, n.Method, n.AlterID = "vmess", o.s("uuid"), firstNonEmpty(o.s("security"), "auto"), num(o["alter_id"])
	case "trojan":
		n.Proto, n.Password = "trojan", o.s("password")
	case "shadowsocks":
		n.Proto, n.Method, n.Password = "shadowsocks", o.s("method"), o.s("password")
		n.Plugin, n.PluginOpts = o.s("plugin"), o.s("plugin_opts")
	case "hysteria2":
		n.Proto, n.Password = "hysteria2", o.s("password")
		if ob := o.m("obfs"); len(ob) > 0 {
			n.Obfs, n.ObfsPassword = ob.s("type"), ob.s("password")
		}
		n.UpMbps, n.DownMbps = num(o["up_mbps"]), num(o["down_mbps"])
		if ports := o.list("server_ports"); len(ports) > 0 {
			n.PortHop = strings.ReplaceAll(strings.Join(ports, ","), ":", "-")
		}
	case "hysteria":
		n.Proto, n.Password, n.Obfs = "hysteria", firstNonEmpty(o.s("auth_str"), o.s("auth")), o.s("obfs")
		n.UpMbps, n.DownMbps = num(o["up_mbps"]), num(o["down_mbps"])
	case "tuic":
		n.Proto, n.UUID, n.Password = "tuic", o.s("uuid"), o.s("password")
		n.CongestionControl, n.UDPRelayMode = o.s("congestion_control"), o.s("udp_relay_mode")
	case "anytls":
		n.Proto, n.Password, n.Security = "anytls", o.s("password"), "tls"
	case "ssh":
		n.Proto, n.Username, n.Password = "ssh", o.s("user"), o.s("password")
		if keys := o.list("host_key"); len(keys) > 0 {
			n.HostKey = keys[0]
		}
	case "socks":
		n.Proto, n.Username, n.Password = "socks", o.s("username"), o.s("password")
	case "http":
		n.Proto, n.Username, n.Password = "http", o.s("username"), o.s("password")
	case "wireguard":
		n.Proto = "wireguard"
		n.PrivateKey = o.s("private_key")
		n.LocalAddress = append(o.list("local_address"), o.list("address")...)
		n.MTU = num(o["mtu"])
		n.PeerPublicKey, n.PreSharedKey = o.s("peer_public_key"), o.s("pre_shared_key")
		n.Reserved = ints(o["reserved"])
		if peers, ok := o["peers"].([]any); ok && len(peers) > 0 {
			if p, ok := peers[0].(map[string]any); ok {
				pm := cmap(p)
				n.Server = firstNonEmpty(pm.s("server"), pm.s("address"), n.Server)
				if port := num(firstAny(pm["server_port"], pm["port"])); port > 0 {
					n.Port = port
				}
				n.PeerPublicKey = firstNonEmpty(pm.s("public_key"), n.PeerPublicKey)
				n.PreSharedKey = firstNonEmpty(pm.s("pre_shared_key"), n.PreSharedKey)
				if r := ints(pm["reserved"]); len(r) == 3 {
					n.Reserved = r
				}
			}
		}
	default:
		return nil
	}
	return n
}

// xrayOutbound reads one Xray outbound; all is its config's outbounds, for
// a fragmenting freedom outbound it dials through.
func xrayOutbound(o cmap, all []any) *Node {
	set := o.m("settings")
	n := &Node{Name: firstNonEmpty(o.s("tag"), o.s("remarks"))}
	// The server: the old form's vnext or servers list, or the settings
	// themselves in the newer flat form.
	srv := set
	if list, ok := firstAny(set["vnext"], set["servers"]).([]any); ok && len(list) > 0 {
		if m, ok := list[0].(map[string]any); ok {
			srv = m
		}
	}
	n.Server, n.Port = srv.s("address"), num(srv["port"])
	user := srv
	if users, ok := srv["users"].([]any); ok && len(users) > 0 {
		if m, ok := users[0].(map[string]any); ok {
			user = m
		}
	}
	switch o.s("protocol") {
	case "vless":
		n.Proto, n.UUID, n.Flow, n.Encryption = "vless", user.s("id"), user.s("flow"), user.s("encryption")
	case "vmess":
		n.Proto, n.UUID, n.Method, n.AlterID = "vmess", user.s("id"), firstNonEmpty(user.s("security"), "auto"), num(user["alterId"])
	case "trojan":
		n.Proto, n.Password = "trojan", srv.s("password")
	case "shadowsocks":
		n.Proto, n.Method, n.Password = "shadowsocks", srv.s("method"), srv.s("password")
	case "socks":
		n.Proto, n.Username, n.Password = "socks", user.s("user"), user.s("pass")
	case "http":
		n.Proto, n.Username, n.Password = "http", user.s("user"), user.s("pass")
	case "wireguard":
		n.Proto = "wireguard"
		n.PrivateKey = set.s("secretKey")
		n.LocalAddress = set.list("address")
		n.MTU = num(set["mtu"])
		n.Reserved = ints(set["reserved"])
		if peers, ok := set["peers"].([]any); ok && len(peers) > 0 {
			if p, ok := peers[0].(map[string]any); ok {
				pm := cmap(p)
				n.PeerPublicKey, n.PreSharedKey = pm.s("publicKey"), pm.s("preSharedKey")
				if h, port, err := net.SplitHostPort(pm.s("endpoint")); err == nil {
					n.Server, n.Port = h, atoi(port)
				}
			}
		}
	default:
		return nil
	}
	st := o.m("streamSettings")
	n.Transport = st.s("network")
	switch n.Transport {
	case "splithttp":
		n.Transport = "xhttp"
	case "raw":
		n.Transport = "tcp"
	case "http":
		n.Transport = "h2"
	}
	switch st.s("security") {
	case "tls":
		t := st.m("tlsSettings")
		n.Security, n.SNI, n.Insecure, n.ALPN, n.Fingerprint = "tls", t.s("serverName"), t.b("allowInsecure"), t.list("alpn"), t.s("fingerprint")
	case "reality":
		r := st.m("realitySettings")
		n.Security, n.SNI, n.PublicKey, n.ShortID, n.Fingerprint, n.SpiderX = "reality", r.s("serverName"), firstNonEmpty(r.s("publicKey"), r.s("password")), r.s("shortId"), r.s("fingerprint"), r.s("spiderX")
	}
	for _, key := range []string{"wsSettings", "httpupgradeSettings", "xhttpSettings", "splithttpSettings", "httpSettings"} {
		if t := st.m(key); len(t) > 0 {
			n.Path = t.s("path")
			n.Host = firstNonEmpty(t.s("host"), t.m("headers").s("Host", "host"))
			if hosts := t.list("host"); n.Host == "" && len(hosts) > 0 {
				n.Host = hosts[0]
			}
			n.Mode = t.s("mode")
			if x, ok := t["extra"].(map[string]any); ok {
				if b, err := json.Marshal(x); err == nil {
					n.Extra = string(b)
				}
			}
		}
	}
	if g := st.m("grpcSettings"); len(g) > 0 {
		n.ServiceName = g.s("serviceName")
		if g.b("multiMode") {
			n.Mode = "multi"
		}
	}
	if k := st.m("kcpSettings"); len(k) > 0 {
		n.Seed, n.HeaderType = k.s("seed"), k.m("header").s("type")
	}
	tcp := st.m("tcpSettings")
	if len(tcp) == 0 {
		tcp = st.m("rawSettings")
	}
	if h := tcp.m("header"); h.s("type") == "http" {
		n.HeaderType = "http"
		req := h.m("request")
		if p := req.list("path"); len(p) > 0 {
			n.Path = p[0]
		}
		if hs := req.m("headers").list("Host"); len(hs) > 0 {
			n.Host = hs[0]
		}
	}
	// Dialled through a freedom outbound that fragments the TLS hello: the
	// app fragments it itself.
	if d := st.m("sockopt").s("dialerProxy"); d != "" {
		for _, x := range all {
			if f, ok := x.(map[string]any); ok && cmap(f).s("tag") == d && cmap(f).s("protocol") == "freedom" && len(cmap(f).m("settings").m("fragment")) > 0 {
				n.Fragment = true
			}
		}
	}
	return n
}

// ParseWGConf reads WireGuard's INI configs, AmneziaWG's among them (its
// Jc, Jmin, Jmax, S1, S2 and H1 to H4 keys); text may hold several.
func ParseWGConf(text string) []*Node {
	var out []*Node
	parts := strings.Split(text, "[Interface]")
	for _, part := range parts[1:] {
		n := &Node{Proto: "wireguard"}
		awg := &AmneziaWG{}
		isAWG := false
		section := "interface"
		for _, raw := range strings.Split(part, "\n") {
			line := strings.TrimSpace(raw)
			if line == "" || line[0] == '#' || line[0] == ';' {
				continue
			}
			if strings.HasPrefix(line, "[") {
				section = strings.ToLower(strings.Trim(line, "[]"))
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
			switch section + "." + k {
			case "interface.privatekey":
				n.PrivateKey = v
			case "interface.address":
				n.LocalAddress = append(n.LocalAddress, splitList(v)...)
			case "interface.mtu":
				n.MTU = atoi(v)
			case "interface.jc", "interface.jmin", "interface.jmax", "interface.s1", "interface.s2":
				isAWG = true
				switch k {
				case "jc":
					awg.Jc = atoi(v)
				case "jmin":
					awg.Jmin = atoi(v)
				case "jmax":
					awg.Jmax = atoi(v)
				case "s1":
					awg.S1 = atoi(v)
				case "s2":
					awg.S2 = atoi(v)
				}
			case "interface.h1", "interface.h2", "interface.h3", "interface.h4":
				isAWG = true
				switch k {
				case "h1":
					awg.H1 = v
				case "h2":
					awg.H2 = v
				case "h3":
					awg.H3 = v
				case "h4":
					awg.H4 = v
				}
			case "peer.publickey":
				if n.PeerPublicKey == "" {
					n.PeerPublicKey = v
				}
			case "peer.presharedkey":
				n.PreSharedKey = v
			case "peer.endpoint":
				if n.Server == "" {
					if h, p, err := net.SplitHostPort(v); err == nil {
						n.Server, n.Port = h, atoi(p)
					}
				}
			case "peer.reserved":
				n.Reserved = parseReserved(v)
			}
		}
		if isAWG {
			n.Proto, n.AWG = "amneziawg", awg
		}
		n.Name = n.Proto + " " + net.JoinHostPort(n.Server, strconv.Itoa(n.Port))
		out = append(out, n)
	}
	return out
}

// ints reads a JSON array of numbers (or a "1,2,3" string).
func ints(v any) []int {
	switch x := v.(type) {
	case []any:
		var out []int
		for _, e := range x {
			out = append(out, num(e))
		}
		return out
	case string:
		return parseReserved(x)
	}
	return nil
}

func firstAny(vs ...any) any {
	for _, v := range vs {
		if v != nil {
			return v
		}
	}
	return nil
}
