package node

import "strings"

// Surge keeps its servers as lines too, in a different shape from Quantumult
// X: the type, host and port come first, then named options.
//
//	<name> = <type>, <host>, <port>, key=value, ...
//
// Loon's proxy lines look almost the same, so many of them parse here as
// well. A line this cannot build is left out.

var surgeTypes = map[string]string{
	"vmess": "vmess", "vless": "vless", "trojan": "trojan", "ss": "shadowsocks",
	"shadowsocks": "shadowsocks", "http": "http", "https": "http", "socks5": "socks",
	"socks5-tls": "socks", "hysteria2": "hysteria2", "hysteria": "hysteria", "tuic": "tuic",
	"tuic-v5": "tuic", "anytls": "anytls",
}

// ParseSurge reads the proxy lines of a Surge configuration (or a bare list
// of them) and returns the nodes it can build.
func ParseSurge(text string) []*Node {
	var out []*Node
	for _, line := range strings.Split(text, "\n") {
		if n := surgeLine(line); n != nil {
			out = append(out, n)
		}
	}
	return out
}

// surgeHint reports whether a text looks like Surge proxy lines.
func surgeHint(text string) bool {
	// A "= type," after a name, for a type Surge writes; cheap to miss on.
	for _, k := range []string{"= vmess,", "= trojan,", "= ss,", "= vless,", "= hysteria2,", "= tuic,", "=vmess,", "=trojan,", "=ss,", "[Proxy]"} {
		if strings.Contains(text, k) {
			return true
		}
	}
	return false
}

func surgeLine(line string) *Node {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "[") {
		return nil
	}
	eq := strings.IndexByte(line, '=')
	if eq <= 0 {
		return nil
	}
	name := strings.TrimSpace(line[:eq])
	args := splitTrim(line[eq+1:])
	if len(args) < 3 {
		return nil
	}
	proto, ok := surgeTypes[strings.ToLower(args[0])]
	if !ok {
		return nil
	}
	host, port := args[1], atoi(args[2])
	if host == "" || port == 0 {
		return nil
	}
	n := &Node{Proto: proto, Server: host, Port: port, Name: cleanName(name)}
	kv := map[string]string{}
	var positional []string
	for _, a := range args[3:] {
		if i := strings.IndexByte(a, '='); i > 0 {
			kv[strings.ToLower(strings.TrimSpace(a[:i]))] = strings.Trim(strings.TrimSpace(a[i+1:]), `"`)
		} else {
			positional = append(positional, a)
		}
	}
	tls := kv["tls"] == "true" || proto == "trojan" || proto == "hysteria2" || proto == "tuic" || proto == "anytls" || strings.HasSuffix(args[0], "-tls")
	setWS := func() {
		if kv["ws"] == "true" {
			n.Transport = "ws"
			n.Path = firstNonEmpty(kv["ws-path"], "/")
			if h := kv["ws-headers"]; h != "" {
				if _, v, ok := strings.Cut(h, ":"); ok {
					n.Host = strings.TrimSpace(v)
				}
			}
		}
	}
	switch proto {
	case "vmess", "vless":
		n.UUID = kv["username"]
		if proto == "vless" {
			n.Encryption = firstNonEmpty(kv["encryption"], "none")
			n.Flow = kv["flow"]
		} else {
			n.Method = "auto"
		}
		setWS()
	case "trojan":
		n.Password = kv["password"]
		setWS()
	case "shadowsocks":
		n.Method, n.Password = kv["encrypt-method"], kv["password"]
		if o := strings.ToLower(kv["obfs"]); o == "http" || o == "tls" {
			n.Plugin, n.PluginOpts = "obfs-local", "obfs="+o
			if h := kv["obfs-host"]; h != "" {
				n.PluginOpts += ";obfs-host=" + h
			}
		}
		tls = false
	case "http", "socks":
		// Surge writes the username and the password as plain positional
		// values: type, host, port, username, password.
		n.Username = firstNonEmpty(kv["username"], get(positional, 0))
		n.Password = firstNonEmpty(kv["password"], get(positional, 1))
	case "hysteria2", "hysteria", "tuic", "anytls":
		n.Password = firstNonEmpty(kv["password"], kv["auth"], kv["token"])
		n.UUID = kv["uuid"]
	}
	if tls {
		n.Security = "tls"
		n.SNI = firstNonEmpty(kv["sni"], kv["server-name"], n.Host)
	}
	if kv["skip-cert-verify"] == "true" || kv["allow-insecure"] == "true" {
		n.Insecure = true
	}
	n.normalize()
	if n.Validate() != nil {
		return nil
	}
	return n
}

// splitTrim splits on commas and trims each part.
func splitTrim(s string) []string {
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func get(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return ""
}
