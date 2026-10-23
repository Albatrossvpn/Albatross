package node

import (
	"encoding/json"
	"strings"
)

// plaintextAddr is a private address Xray accepts for unencrypted VLESS and
// Trojan; the connection itself goes to the real server through a freedom
// outbound that redirects there.
const plaintextAddr = "10.255.255.254"

// Xray returns the Xray-core outbounds for nodes sing-box cannot run (xhttp,
// mKCP, VLESS post-quantum encryption, ECH, pinned certificates). The first
// outbound carries tag; a second one may follow as its dialer.
func (n *Node) Xray(tag string) []map[string]any {
	out := n.xrayOutbound(tag)
	if out == nil {
		return nil
	}
	if !n.xrayPlaintext() {
		return []map[string]any{out}
	}
	// Xray refuses VLESS/Trojan without TLS to public addresses. Point the
	// outbound at a private address and dial the real server via freedom.
	settings := out["settings"].(map[string]any)
	settings["address"] = plaintextAddr
	stream := out["streamSettings"].(map[string]any)
	stream["sockopt"] = map[string]any{"dialerProxy": tag + "-dial"}
	for _, key := range []string{"xhttpSettings", "wsSettings", "httpupgradeSettings"} {
		if t, ok := stream[key].(map[string]any); ok {
			if _, has := t["host"]; !has && !isIP(n.Server) {
				t["host"] = n.Server
			}
		}
	}
	if g, ok := stream["grpcSettings"].(map[string]any); ok {
		if _, has := g["authority"]; !has && !isIP(n.Server) {
			g["authority"] = n.Server
		}
	}
	dial := map[string]any{
		"tag":      tag + "-dial",
		"protocol": "freedom",
		"settings": map[string]any{"redirect": n.Endpoint()},
	}
	return []map[string]any{out, dial}
}

func (n *Node) xrayPlaintext() bool {
	if n.Security != "" {
		return false
	}
	switch n.Proto {
	case "vless":
		return n.Encryption == "" || n.Encryption == "none"
	case "trojan":
		return true
	}
	return false
}

func (n *Node) xrayOutbound(tag string) map[string]any {
	out := map[string]any{"tag": tag}
	switch n.Proto {
	case "vless":
		enc := n.Encryption
		if enc == "" {
			enc = "none"
		}
		s := map[string]any{"address": n.Server, "port": n.Port, "id": n.UUID, "encryption": enc}
		if n.Flow != "" {
			s["flow"] = n.Flow
		}
		out["protocol"] = "vless"
		out["settings"] = s
	case "vmess":
		out["protocol"] = "vmess"
		out["settings"] = map[string]any{"address": n.Server, "port": n.Port, "id": n.UUID, "security": n.Method}
	case "trojan":
		out["protocol"] = "trojan"
		out["settings"] = map[string]any{"address": n.Server, "port": n.Port, "password": n.Password}
	case "shadowsocks":
		out["protocol"] = "shadowsocks"
		out["settings"] = map[string]any{"address": n.Server, "port": n.Port, "method": n.Method, "password": n.Password}
	default:
		return nil
	}
	out["streamSettings"] = n.xrayStream()
	return out
}

// XraySupportsMethod reports whether Xray can use this shadowsocks cipher.
func XraySupportsMethod(m string) bool { return xraySSMethods[m] }

func (n *Node) xrayStream() map[string]any {
	network := n.Transport
	if network == "" {
		network = "raw"
	}
	if network == "tcp" {
		network = "raw"
	}
	s := map[string]any{"network": network}
	switch network {
	case "raw":
		if n.HeaderType == "http" {
			req := map[string]any{}
			if p := n.Path; p != "" {
				req["path"] = splitList(p)
			}
			if h := splitList(n.Host); len(h) > 0 {
				req["headers"] = map[string]any{"Host": h}
			}
			s["rawSettings"] = map[string]any{"header": map[string]any{"type": "http", "request": req}}
		}
	case "ws":
		ws := map[string]any{}
		if n.Path != "" {
			ws["path"] = n.Path
		}
		if n.EarlyData > 0 {
			sep := "?"
			if strings.Contains(n.Path, "?") {
				sep = "&"
			}
			ws["path"] = n.Path + sep + "ed=" + itoa(n.EarlyData)
		}
		if h := firstHost(n.Host); h != "" {
			ws["host"] = h
		}
		s["wsSettings"] = ws
	case "httpupgrade":
		hu := map[string]any{}
		if n.Path != "" {
			hu["path"] = n.Path
		}
		if h := firstHost(n.Host); h != "" {
			hu["host"] = h
		}
		s["httpupgradeSettings"] = hu
	case "grpc":
		g := map[string]any{"serviceName": n.ServiceName}
		if n.Mode == "multi" {
			g["multiMode"] = true
		}
		if n.Host != "" {
			g["authority"] = firstHost(n.Host)
		}
		s["grpcSettings"] = g
	case "xhttp":
		x := map[string]any{}
		if n.Path != "" {
			x["path"] = n.Path
		}
		if h := firstHost(n.Host); h != "" {
			x["host"] = h
		}
		if n.Mode != "" {
			x["mode"] = n.Mode
		}
		if n.Extra != "" {
			var extra map[string]any
			if json.Unmarshal([]byte(n.Extra), &extra) == nil {
				x["extra"] = extra
			}
		}
		s["xhttpSettings"] = x
	case "kcp":
		k := map[string]any{}
		if n.Seed != "" {
			k["seed"] = n.Seed
		}
		if n.HeaderType != "" && n.HeaderType != "none" {
			k["header"] = map[string]any{"type": n.HeaderType}
		}
		s["kcpSettings"] = k
	case "http":
		// Xray folded h2 into xhttp; "stream-one" over TLS with h2 is the
		// equivalent wire format.
		s["network"] = "xhttp"
		x := map[string]any{"mode": "stream-one"}
		if n.Path != "" {
			x["path"] = n.Path
		}
		if h := firstHost(n.Host); h != "" {
			x["host"] = h
		}
		s["xhttpSettings"] = x
	}
	switch n.Security {
	case "tls":
		t := map[string]any{}
		if sni := n.serverName(); sni != "" {
			t["serverName"] = sni
		}
		if len(n.ALPN) > 0 {
			t["alpn"] = n.ALPN
		}
		fp := n.fp()
		if fp == "" {
			fp = "chrome"
		}
		t["fingerprint"] = fp
		if n.ECH != "" {
			t["echConfigList"] = n.ECH
		}
		if n.PinnedCert != "" {
			t["pinnedPeerCertSha256"] = n.PinnedCert
		}
		if n.VerifyName != "" {
			t["verifyPeerCertByName"] = n.VerifyName
		}
		s["security"] = "tls"
		s["tlsSettings"] = t
	case "reality":
		r := map[string]any{
			"serverName":  n.serverName(),
			"fingerprint": n.fp(),
			"publicKey":   n.PublicKey,
			"shortId":     n.ShortID,
		}
		if n.SpiderX != "" {
			r["spiderX"] = n.SpiderX
		}
		s["security"] = "reality"
		s["realitySettings"] = r
	default:
		s["security"] = "none"
	}
	if n.FinalMask != "" {
		var fm map[string]any
		if json.Unmarshal([]byte(n.FinalMask), &fm) == nil {
			s["finalmask"] = fm
		}
	}
	return s
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	p := len(b)
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		b[p] = '-'
	}
	return string(b[p:])
}
