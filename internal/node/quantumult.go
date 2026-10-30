package node

import "strings"

// Quantumult X keeps its servers as lines, not share links, so no scheme
// regex finds them. The lists we collect carry plenty of them. Each line is
//
//	<type>=<host>:<port>, key=value, ..., tag=<name>
//
// for vmess, vless, trojan, shadowsocks (ss) and the plain proxies. Loon
// and Surge's "ProxyName = type, host, port, key=value" are close cousins;
// this reads the Quantumult X form, the common one in our lists.

var qxTypes = map[string]string{
	"vmess": "vmess", "vless": "vless", "trojan": "trojan",
	"shadowsocks": "shadowsocks", "ss": "shadowsocks", "http": "http", "socks5": "socks",
}

// ParseQuantumult reads the server lines of a Quantumult X configuration and
// returns the nodes it can build; a line it cannot is left out.
func ParseQuantumult(text string) []*Node {
	var out []*Node
	for _, line := range strings.Split(text, "\n") {
		if n := quantumultLine(line); n != nil {
			out = append(out, n)
		}
	}
	return out
}

// quantumultHint reports whether a text looks like Quantumult X server lines,
// so EachText only spends the pass when it is worth it.
func quantumultHint(text string) bool {
	for _, k := range []string{"vmess=", "vless=", "trojan=", "shadowsocks=", "ss=", "[server_local]"} {
		if strings.Contains(text, k) {
			return true
		}
	}
	return false
}

func quantumultLine(line string) *Node {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "[") {
		return nil
	}
	eq := strings.IndexByte(line, '=')
	if eq <= 0 {
		return nil
	}
	proto, ok := qxTypes[strings.ToLower(strings.TrimSpace(line[:eq]))]
	if !ok {
		return nil
	}
	parts := strings.Split(line[eq+1:], ",")
	host, port, err := splitHostPort(strings.TrimSpace(parts[0]))
	if err != nil || host == "" || atoi(port) == 0 {
		return nil
	}
	kv := map[string]string{}
	for _, p := range parts[1:] {
		if i := strings.IndexByte(p, '='); i > 0 {
			kv[strings.ToLower(strings.TrimSpace(p[:i]))] = strings.TrimSpace(p[i+1:])
		}
	}
	n := &Node{Proto: proto, Server: host, Port: atoi(port), Name: cleanName(kv["tag"])}
	obfs := strings.ToLower(kv["obfs"])
	tls := kv["over-tls"] == "true" || kv["tls"] == "true" || obfs == "over-tls" || obfs == "wss" || obfs == "grpc+tls"
	host7 := firstNonEmpty(kv["tls-host"], kv["obfs-host"], kv["sni"])
	switch proto {
	case "vmess", "vless":
		if proto == "vmess" {
			n.UUID = kv["password"]
			n.Method = firstNonEmpty(kv["method"], "auto")
		} else {
			n.UUID = kv["password"]
			n.Encryption = firstNonEmpty(kv["method"], "none")
		}
		switch {
		case obfs == "ws" || obfs == "wss":
			n.Transport, n.Host, n.Path = "ws", kv["obfs-host"], firstNonEmpty(kv["obfs-uri"], "/")
		case obfs == "http":
			n.Transport, n.Host, n.Path = "http", kv["obfs-host"], firstNonEmpty(kv["obfs-uri"], "/")
		case strings.HasPrefix(obfs, "grpc"):
			n.Transport, n.ServiceName = "grpc", kv["obfs-uri"]
		default:
			n.Transport = "tcp"
		}
		if tls {
			n.Security, n.SNI = "tls", host7
		}
	case "trojan":
		n.Password = kv["password"]
		n.Security, n.SNI = "tls", host7
		if obfs == "ws" || obfs == "wss" {
			n.Transport, n.Host, n.Path = "ws", kv["obfs-host"], firstNonEmpty(kv["obfs-uri"], "/")
		} else if strings.HasPrefix(obfs, "grpc") {
			n.Transport, n.ServiceName = "grpc", kv["obfs-uri"]
		}
	case "shadowsocks":
		n.Method, n.Password = kv["method"], kv["password"]
		if obfs == "http" || obfs == "tls" {
			n.Plugin, n.PluginOpts = "obfs-local", "obfs="+obfs
			if h := kv["obfs-host"]; h != "" {
				n.PluginOpts += ";obfs-host=" + h
			}
		}
	case "http", "socks":
		n.Username, n.Password = kv["username"], kv["password"]
		if tls {
			n.Security, n.SNI = "tls", host7
		}
	}
	if kv["tls-verification"] == "false" || kv["skip-cert-verify"] == "true" {
		n.Insecure = true
	}
	n.normalize()
	if n.Validate() != nil {
		return nil
	}
	return n
}
