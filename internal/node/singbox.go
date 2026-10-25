package node

import (
	"net"
	"strings"
)

// SingBoxOptions adjusts generated sing-box outbounds.
type SingBoxOptions struct {
	// Fragment splits the TLS ClientHello across TCP segments and TLS
	// records, which gets past SNI filters that do not reassemble.
	Fragment bool
	// Spoof sends a forged ClientHello with this allowed SNI first, which
	// fools whitelist filters. Needs administrator rights.
	Spoof string
	// LineMbps is the speed measured on this network (0 = unknown). Hysteria2
	// servers without their own numbers are asked to send at about this
	// rate (Brutal), which holds up on lossy mobile lines where the default
	// congestion control backs off.
	LineMbps int
}

// SingBox returns the sing-box object for this node. WireGuard and OpenVPN
// are endpoints in sing-box; everything else is an outbound.
func (n *Node) SingBox(tag string, o SingBoxOptions) (obj map[string]any, endpoint bool) {
	out := map[string]any{"tag": tag}
	switch n.Proto {
	case "vmess":
		out["type"] = "vmess"
		n.sbServer(out)
		out["uuid"] = n.UUID
		out["security"] = n.Method
		if n.AlterID > 0 {
			out["alter_id"] = n.AlterID
		}
		n.sbStream(out, o)
	case "vless":
		out["type"] = "vless"
		n.sbServer(out)
		out["uuid"] = n.UUID
		if n.Flow != "" {
			out["flow"] = "xtls-rprx-vision"
		}
		out["packet_encoding"] = "xudp"
		n.sbStream(out, o)
	case "trojan":
		out["type"] = "trojan"
		n.sbServer(out)
		out["password"] = n.Password
		n.sbStream(out, o)
	case "shadowsocks":
		out["type"] = "shadowsocks"
		n.sbServer(out)
		out["method"] = n.Method
		out["password"] = n.Password
		if n.Plugin == "shadow-tls" {
			out["detour"] = tag + "-stls"
			// The ShadowTLS detour carries TCP only; UDP (QUIC, games,
			// calls) rides inside it as sing-box servers allow.
			out["udp_over_tcp"] = true
		} else if n.Plugin != "" {
			out["plugin"] = n.Plugin
			if n.PluginOpts != "" {
				out["plugin_opts"] = n.PluginOpts
			}
		}
	case "hysteria2":
		out["type"] = "hysteria2"
		n.sbServerHop(out)
		out["password"] = n.Password
		if n.Obfs == "salamander" || n.ObfsPassword != "" {
			out["obfs"] = map[string]any{"type": "salamander", "password": n.ObfsPassword}
		}
		up, down := n.UpMbps, n.DownMbps
		if up <= 0 && down <= 0 && o.LineMbps > 0 {
			// A little above what was measured, since the measurement went
			// through servers slower than the line; upload is rarely
			// measured and usually far slower.
			down = min(max(o.LineMbps*5/4, 10), 1000)
			up = min(max(o.LineMbps/4, 5), 50)
		}
		if up > 0 {
			out["up_mbps"] = up
		}
		if down > 0 {
			out["down_mbps"] = down
		}
		out["tls"] = n.sbQUICTLS(nil)
	case "hysteria":
		out["type"] = "hysteria"
		n.sbServerHop(out)
		up, down := n.UpMbps, n.DownMbps
		if up <= 0 {
			up = 20
		}
		if down <= 0 {
			down = 100
		}
		out["up_mbps"] = up
		out["down_mbps"] = down
		if n.ObfsPassword != "" {
			out["obfs"] = n.ObfsPassword
		}
		if n.Password != "" {
			out["auth_str"] = n.Password
		}
		out["tls"] = n.sbQUICTLS([]string{"hysteria"})
	case "tuic":
		out["type"] = "tuic"
		n.sbServer(out)
		out["uuid"] = n.UUID
		out["password"] = n.Password
		switch n.CongestionControl {
		case "bbr", "cubic", "new_reno":
			out["congestion_control"] = n.CongestionControl
		}
		if n.UDPRelayMode == "quic" {
			out["udp_relay_mode"] = "quic"
		}
		out["tls"] = n.sbQUICTLS([]string{"h3"})
	case "anytls":
		out["type"] = "anytls"
		n.sbServer(out)
		out["password"] = n.Password
		out["tls"] = n.sbTLS(o)
	case "direct":
		// Nothing to connect to. The ClientHello of every TLS connection is
		// split so a name-based filter does not see the name whole; sing-box
		// takes that as a route option (core.Set.Direct), not here.
		out["type"] = "direct"
		return out, false
	case "ssh":
		out["type"] = "ssh"
		n.sbServer(out)
		out["user"] = n.Username
		out["password"] = n.Password
		if n.HostKey != "" {
			out["host_key"] = []string{n.HostKey}
		}
	case "socks":
		out["type"] = "socks"
		n.sbServer(out)
		out["version"] = "5"
		if n.Username != "" || n.Password != "" {
			out["username"] = n.Username
			out["password"] = n.Password
		}
	case "wireguard":
		out["type"] = "wireguard"
		mtu := n.MTU
		if mtu <= 0 {
			mtu = 1280
		}
		out["mtu"] = mtu
		out["address"] = n.LocalAddress
		out["private_key"] = n.PrivateKey
		peer := map[string]any{
			"address":     n.Server,
			"port":        n.Port,
			"public_key":  n.PeerPublicKey,
			"allowed_ips": []string{"0.0.0.0/0", "::/0"},
		}
		if n.PreSharedKey != "" {
			peer["pre_shared_key"] = n.PreSharedKey
		}
		if len(n.Reserved) == 3 {
			peer["reserved"] = n.Reserved
		}
		out["peers"] = []any{peer}
		return out, true
	case "openvpn":
		return n.sbOpenVPN(out), true
	}
	return out, false
}

func (n *Node) sbServer(out map[string]any) {
	out["server"] = n.Server
	out["server_port"] = n.Port
}

func (n *Node) sbServerHop(out map[string]any) {
	out["server"] = n.Server
	if n.PortHop == "" {
		out["server_port"] = n.Port
		return
	}
	var ranges []string
	for _, p := range splitList(n.PortHop) {
		lo, hi, ok := strings.Cut(p, "-")
		if !ok {
			hi = lo
		}
		if atoi(lo) > 0 && atoi(hi) >= atoi(lo) {
			ranges = append(ranges, strings.TrimSpace(lo)+":"+strings.TrimSpace(hi))
		}
	}
	if len(ranges) == 0 {
		out["server_port"] = n.Port
		return
	}
	out["server_ports"] = ranges
	out["hop_interval"] = "30s"
}

func (n *Node) sbStream(out map[string]any, o SingBoxOptions) {
	if t := n.sbTLS(o); t != nil {
		out["tls"] = t
	}
	if t := n.sbTransport(); t != nil {
		out["transport"] = t
	}
}

func isIP(s string) bool { return net.ParseIP(s) != nil }

// serverName follows Xray's choice: explicit SNI, else the HTTP host for
// HTTP-like transports, else the server address when it is a name.
func (n *Node) serverName() string {
	if n.SNI != "" {
		return n.SNI
	}
	switch n.Transport {
	case "ws", "httpupgrade", "http", "xhttp", "grpc":
		if h := firstHost(n.Host); h != "" && !isIP(h) {
			return h
		}
	}
	if !isIP(n.Server) {
		return n.Server
	}
	return ""
}

func firstHost(h string) string {
	if i := strings.Index(h, ","); i >= 0 {
		h = h[:i]
	}
	return strings.TrimSpace(h)
}

func (n *Node) sbTLS(o SingBoxOptions) map[string]any {
	if n.Security != "tls" && n.Security != "reality" {
		return nil
	}
	t := map[string]any{"enabled": true}
	if sni := n.serverName(); sni != "" {
		t["server_name"] = sni
	}
	if n.Insecure || (n.Security == "tls" && n.serverName() == "") {
		t["insecure"] = true
	}
	alpn := n.tcpALPN()
	fp := n.fp()
	if n.Fingerprint == "" && n.Security == "tls" {
		// Go's own ClientHello is easy for filtering to single out; look
		// like a browser (Chrome unless the app picked another), as Xray
		// does by default.
		if fp == "" {
			fp = "chrome"
		}
		if len(alpn) == 0 && (n.Transport == "ws" || n.Transport == "httpupgrade") {
			// A browser's hello offers h2, which these transports cannot use.
			alpn = []string{"http/1.1"}
		}
	}
	if len(alpn) > 0 {
		t["alpn"] = alpn
	}
	ech := n.TryECH && n.Security == "tls" && n.ECH == ""
	if ech {
		// The real name travels encrypted inside a hello for the CDN's
		// public name, so SNI filtering sees nothing to block. uTLS cannot
		// do ECH; Go's own TLS stack does.
		t["ech"] = map[string]any{"enabled": true}
		fp = ""
	}
	if fp != "" {
		t["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
	}
	if o.Spoof != "" && n.Security == "tls" {
		t["spoof"] = o.Spoof
	}
	if n.Security == "reality" {
		t["reality"] = map[string]any{"enabled": true, "public_key": n.PublicKey, "short_id": n.ShortID}
		if fp == "" {
			t["utls"] = map[string]any{"enabled": true, "fingerprint": "chrome"}
		}
	} else if !ech {
		if n.Fragment || o.Fragment {
			t["fragment"] = true
		}
		if o.Fragment {
			t["record_fragment"] = true
		}
	}
	return t
}

// tcpALPN drops protocols a TCP transport cannot use. WebSocket and
// HTTPUpgrade need HTTP/1.1, and nothing over TCP can use h3.
func (n *Node) tcpALPN() []string {
	var out []string
	for _, a := range n.ALPN {
		switch a {
		case "h3":
			continue
		case "h2":
			if n.Transport == "ws" || n.Transport == "httpupgrade" {
				continue
			}
		}
		out = append(out, a)
	}
	if len(out) == 0 && (n.Transport == "ws" || n.Transport == "httpupgrade") && len(n.ALPN) > 0 {
		out = []string{"http/1.1"}
	}
	return out
}

func (n *Node) sbQUICTLS(defaultALPN []string) map[string]any {
	t := map[string]any{"enabled": true}
	if sni := n.serverName(); sni != "" {
		t["server_name"] = sni
	}
	if n.Insecure || n.serverName() == "" {
		t["insecure"] = true
	}
	if n.DisableSNI {
		t["disable_sni"] = true
	}
	alpn := n.ALPN
	if len(alpn) == 0 {
		alpn = defaultALPN
	}
	if len(alpn) > 0 {
		t["alpn"] = alpn
	}
	return t
}

func (n *Node) sbTransport() map[string]any {
	switch n.Transport {
	case "ws":
		t := map[string]any{"type": "ws"}
		if n.Path != "" {
			t["path"] = n.Path
		}
		host := firstHost(n.Host)
		if host == "" && n.SNI != "" && !isIP(n.SNI) {
			host = n.SNI
		}
		if host != "" {
			t["headers"] = map[string]any{"Host": host}
		}
		if n.EarlyData > 0 {
			t["max_early_data"] = n.EarlyData
			t["early_data_header_name"] = "Sec-WebSocket-Protocol"
		}
		return t
	case "httpupgrade":
		t := map[string]any{"type": "httpupgrade"}
		if h := firstHost(n.Host); h != "" {
			t["host"] = h
		} else if n.SNI != "" && !isIP(n.SNI) {
			t["host"] = n.SNI
		}
		if n.Path != "" {
			t["path"] = n.Path
		}
		return t
	case "grpc":
		t := map[string]any{"type": "grpc"}
		if n.ServiceName != "" {
			t["service_name"] = n.ServiceName
		}
		return t
	case "http":
		t := map[string]any{"type": "http"}
		if hosts := splitList(n.Host); len(hosts) > 0 {
			t["host"] = hosts
		}
		if n.Path != "" {
			t["path"] = n.Path
		}
		return t
	case "quic":
		return map[string]any{"type": "quic"}
	case "tcp":
		if n.HeaderType != "http" {
			return nil
		}
		t := map[string]any{"type": "http", "method": "GET"}
		if hosts := splitList(n.Host); len(hosts) > 0 {
			t["host"] = hosts
		}
		if paths := splitList(n.Path); len(paths) > 0 {
			t["path"] = paths[0]
		}
		return t
	}
	return nil
}

func (n *Node) sbOpenVPN(out map[string]any) map[string]any {
	v := n.OVPN
	out["type"] = "openvpn-client"
	out["server"] = n.Server
	out["server_port"] = n.Port
	out["network"] = v.Network
	tls := map[string]any{"certificate": v.CA}
	if len(v.Cert) > 0 && len(v.Key) > 0 {
		tls["client_certificate"] = v.Cert
		tls["client_key"] = v.Key
	}
	if len(v.TLSCrypt) > 0 {
		tls["control_wrap"] = map[string]any{"type": "tls_crypt", "key": v.TLSCrypt}
	} else if len(v.TLSAuth) > 0 {
		w := map[string]any{"type": "tls_auth", "key": v.TLSAuth}
		switch v.KeyDir {
		case "1":
			w["direction"] = "client"
		case "0":
			w["direction"] = "server"
		}
		tls["control_wrap"] = w
	}
	out["tls"] = tls
	ciphers := v.DataCipher
	if len(ciphers) == 0 && v.Cipher != "" {
		ciphers = []string{v.Cipher}
	}
	if len(ciphers) > 0 {
		out["data_ciphers"] = ciphers
	}
	if v.Cipher != "" {
		out["data_ciphers_fallback"] = v.Cipher
	}
	if v.Auth != "" {
		out["auth"] = v.Auth
	}
	if v.Compress != "" {
		out["allow_compression"] = "asym"
	}
	out["route_no_pull"] = true
	return out
}

// SingBoxHelpers returns the extra outbounds a node needs beside its own:
// for Shadowsocks behind ShadowTLS v3, the shadowtls outbound that makes
// the real TLS handshake with the disguise site and that the Shadowsocks
// outbound detours through. The tags end in "-stls".
func (n *Node) SingBoxHelpers(tag string) []map[string]any {
	if n.Proto != "shadowsocks" || n.Plugin != "shadow-tls" {
		return nil
	}
	host := firstNonEmpty(n.pluginOpt("host"), n.SNI, n.Server)
	version := atoi(n.pluginOpt("version"))
	if version == 0 {
		version = 3
	}
	return []map[string]any{{
		"type": "shadowtls", "tag": tag + "-stls",
		"server": n.Server, "server_port": n.Port,
		"version": version, "password": n.pluginOpt("password"),
		"tls": map[string]any{"enabled": true, "server_name": host,
			"utls": map[string]any{"enabled": true, "fingerprint": "chrome"}},
	}}
}
