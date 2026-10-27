package node

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// Sidecar protocols run on a helper program of their own that opens a
// local SOCKS5 port; sing-box then carries the traffic through that port,
// as it does for warp-plus. The helper's name is the binary the app ships
// (core/<name>.exe on Windows, lib<name>.so on Android).
var sidecars = map[string]string{
	"naive":     "naive",          // NaiveProxy: HTTP/2 or HTTP/3 CONNECT behind Caddy, looks like browsing
	"juicity":   "juicity-client", // Juicity: QUIC, TLS 1.3, BBR
	"mieru":     "mieru",          // Mieru: TCP or UDP with its own obfuscation, no TLS to fingerprint
	"brook":     "brook",          // Brook: its own protocol, plain or over WebSocket+TLS
	"wstunnel":  "wstunnel",       // WStunnel: WebSocket over TLS carrying a SOCKS5 tunnel
	"amneziawg": "wireproxy-awg",  // AmneziaWG: WireGuard with junk packets and masked headers
}

// Sidecar names the helper program a node runs on, or "" for cores.
func (n *Node) Sidecar() string { return sidecars[n.Proto] }

// AmneziaWG holds the obfuscation parameters of an AmneziaWG peer.
type AmneziaWG struct {
	Jc   int    `json:"jc"`   // junk packets before each handshake
	Jmin int    `json:"jmin"` // junk packet size range
	Jmax int    `json:"jmax"`
	S1   int    `json:"s1"` // padding of the handshake initiation
	S2   int    `json:"s2"` // padding of the handshake response
	H1   string `json:"h1"` // message type values (a number or "a-b")
	H2   string `json:"h2"`
	H3   string `json:"h3"`
	H4   string `json:"h4"`
}

func (n *Node) validateSidecar() error {
	switch n.Proto {
	case "naive":
		if n.Username == "" || n.Password == "" {
			return fmt.Errorf("naive needs a user and a password")
		}
		if n.Transport != "https" && n.Transport != "quic" {
			return fmt.Errorf("naive runs over https or quic")
		}
		if n.SNI != "" && !plainHost(n.SNI) {
			return fmt.Errorf("bad sni")
		}
	case "juicity":
		if n.UUID == "" || n.Password == "" {
			return fmt.Errorf("juicity needs a uuid and a password")
		}
	case "mieru":
		if n.Username == "" || n.Password == "" {
			return fmt.Errorf("mieru needs a user and a password")
		}
		if n.Transport != "tcp" && n.Transport != "udp" {
			return fmt.Errorf("mieru runs over tcp or udp")
		}
	case "brook":
		if n.Password == "" {
			return fmt.Errorf("brook needs a password")
		}
		if n.Transport != "" && n.Transport != "ws" && n.Transport != "wss" {
			return fmt.Errorf("unsupported brook kind")
		}
	case "wstunnel":
		if n.Path == "" {
			return fmt.Errorf("wstunnel needs the path secret")
		}
	case "amneziawg":
		// Every value goes into the helper's INI file (SidecarArgs), so each
		// is checked for its exact form: a newline in a link from a public
		// list must not add a section of its own (a SOCKS port open to the
		// network, a tunnel to the device's own services).
		if _, ok := wgKey(n.PrivateKey); !ok {
			return fmt.Errorf("amneziawg needs a private key")
		}
		if _, ok := wgKey(n.PeerPublicKey); !ok {
			return fmt.Errorf("amneziawg needs the peer's public key")
		}
		if _, ok := wgKey(n.PreSharedKey); n.PreSharedKey != "" && !ok {
			return fmt.Errorf("bad amneziawg preshared key")
		}
		if len(n.LocalAddress) == 0 {
			return fmt.Errorf("amneziawg needs an address")
		}
		for _, a := range n.LocalAddress {
			if _, err := netip.ParsePrefix(a); err != nil {
				if _, err := netip.ParseAddr(a); err != nil {
					return fmt.Errorf("bad amneziawg address %q", a)
				}
			}
		}
		if !plainHost(n.Server) || n.Port <= 0 || n.Port > 65535 {
			return fmt.Errorf("bad amneziawg endpoint")
		}
		if n.AWG == nil {
			return fmt.Errorf("amneziawg needs its parameters")
		}
		if n.AWG.Jc < 0 || n.AWG.Jc > 128 || n.AWG.Jmin < 0 || n.AWG.Jmax > 1280 || n.AWG.Jmin > n.AWG.Jmax {
			return fmt.Errorf("bad junk packet parameters")
		}
		if n.AWG.S1 < 0 || n.AWG.S1 > 1280 || n.AWG.S2 < 0 || n.AWG.S2 > 1280 {
			return fmt.Errorf("bad padding parameters")
		}
		for _, h := range []string{n.AWG.H1, n.AWG.H2, n.AWG.H3, n.AWG.H4} {
			if h != "" && !awgHeader.MatchString(h) {
				return fmt.Errorf("bad amneziawg header value %q", h)
			}
		}
	default:
		return fmt.Errorf("unsupported protocol %q", n.Proto)
	}
	return nil
}

// hostPort joins the server and port for a command line.
func (n *Node) hostPort() string { return net.JoinHostPort(n.Server, strconv.Itoa(n.Port)) }

// awgHeader is an AmneziaWG message type value: a number, or a range of
// them (AmneziaWG 2).
var awgHeader = regexp.MustCompile(`^[0-9]{1,10}(-[0-9]{1,10})?$`)

// wgKey decodes a WireGuard key (32 bytes in base64, padded or not, standard
// or URL-safe) and returns it in the standard form the configs take.
func wgKey(s string) (string, bool) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == 32 {
			return base64.StdEncoding.EncodeToString(b), true
		}
	}
	return "", false
}

// plainHost says whether s is an IP address or a DNS name and nothing more.
func plainHost(s string) bool {
	if _, err := netip.ParseAddr(s); err == nil {
		return true
	}
	if s == "" || len(s) > 253 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_') {
			return false
		}
	}
	return true
}

// SocksUser is the user name of a helper's local SOCKS5 port when it asks
// for a password (SidecarArgs, UsqueArgs).
const SocksUser = "albatross"

// SidecarAuth reports whether the node's helper can ask for a password on
// its local SOCKS5 port. Brook's client cannot.
func (n *Node) SidecarAuth() bool { return n.Proto != "brook" }

// SidecarArgs are the command line and environment that run the node's
// helper as a SOCKS5 proxy on 127.0.0.1:port. files are written into dir
// (the helper's own directory) first: the configuration files that some
// helpers only read from disk. With pass set (and SidecarAuth) the port
// takes only SocksUser with that password, so other programs on the
// device can neither use the tunnel nor learn the server through it.
func (n *Node) SidecarArgs(port int, dir, pass string) (args, env []string, files map[string]string) {
	bind := "127.0.0.1:" + strconv.Itoa(port)
	auth := ""
	if pass != "" {
		auth = SocksUser + ":" + pass + "@"
	}
	sep := "/"
	if strings.Contains(dir, "\\") {
		sep = "\\"
	}
	files = map[string]string{}
	switch n.Proto {
	case "naive":
		// NaiveProxy takes the TLS name from the proxy's address. With a
		// name of its own (sni=, as a relay's links carry) the address
		// names it and the resolver rules send it to the server.
		host := n.Server
		cfg := map[string]any{"listen": "socks://" + auth + bind}
		if n.SNI != "" && n.SNI != n.Server && !strings.Contains(n.Server, ":") {
			host = n.SNI
			cfg["host-resolver-rules"] = "MAP " + n.SNI + " " + n.Server
		}
		cfg["proxy"] = n.Transport + "://" + urlUser(n.Username) + ":" + urlUser(n.Password) + "@" + net.JoinHostPort(host, strconv.Itoa(n.Port))
		c, _ := json.Marshal(cfg)
		files["config.json"] = string(c)
		args = []string{dir + sep + "config.json"}
	case "juicity":
		// The listener is a URL to Juicity ("mixed://" + listen), whose
		// user part is the SOCKS5 and HTTP login.
		c, _ := json.Marshal(map[string]any{
			"listen": auth + bind, "server": n.hostPort(), "uuid": n.UUID, "password": n.Password,
			"sni": firstNonEmpty(n.SNI, n.Server), "allow_insecure": n.Insecure,
			"congestion_control": firstNonEmpty(n.CongestionControl, "bbr"), "log_level": "warn",
			"pinned_certchain_sha256": n.PinnedCert,
		})
		files["config.json"] = string(c)
		args = []string{"run", "-c", dir + sep + "config.json"}
	case "mieru":
		server := map[string]any{"portBindings": []map[string]any{{"port": n.Port, "protocol": strings.ToUpper(n.Transport)}}}
		if net.ParseIP(n.Server) != nil {
			server["ipAddress"] = n.Server
		} else {
			server["domainName"] = n.Server
		}
		profile := map[string]any{
			"profileName": "albatross", "user": map[string]string{"name": n.Username, "password": n.Password},
			"servers": []map[string]any{server},
		}
		if n.MTU > 0 {
			profile["mtu"] = n.MTU
		}
		cfg := map[string]any{
			"profiles": []map[string]any{profile}, "activeProfile": "albatross",
			"socks5Port": port, "socks5ListenLAN": false, "rpcPort": 0, "loggingLevel": "WARN",
		}
		if pass != "" {
			cfg["socks5Authentication"] = []map[string]string{{"user": SocksUser, "password": pass}}
		}
		c, _ := json.Marshal(cfg)
		files["config.json"] = string(c)
		env = []string{"MIERU_CONFIG_JSON_FILE=" + dir + sep + "config.json"}
		args = []string{"run"}
	case "brook":
		switch n.Transport {
		case "ws":
			args = []string{"wsclient", "--wsserver", "ws://" + n.hostPort() + n.Path, "--password", n.Password, "--socks5", bind}
		case "wss":
			host := firstNonEmpty(n.SNI, n.Host, n.Server)
			args = []string{"wssclient", "--wssserver", "wss://" + net.JoinHostPort(host, strconv.Itoa(n.Port)) + n.Path, "--password", n.Password, "--socks5", bind}
			if host != n.Server {
				args = append(args, "--address", n.hostPort())
			}
			if n.Insecure {
				args = append(args, "--insecure")
			}
		default:
			args = []string{"client", "--server", n.hostPort(), "--password", n.Password, "--socks5", bind}
		}
	case "wstunnel":
		host := firstNonEmpty(n.SNI, n.Server)
		listen := "socks5://" + bind
		if pass != "" {
			listen += "?login=" + SocksUser + "&password=" + pass
		}
		args = []string{"client", "-L", listen, "--http-upgrade-path-prefix", strings.TrimPrefix(n.Path, "/"),
			"--connection-min-idle", "2"}
		if host != n.Server {
			// The server by its address, the name only for the handshake.
			args = append(args, "--tls-sni-override", host, "--http-headers", "Host: "+host)
		}
		if !n.Insecure {
			args = append(args, "--tls-verify-certificate")
		}
		args = append(args, "wss://"+n.hostPort())
	case "amneziawg":
		// The keys in their standard form, rebuilt from their bytes
		// (validateSidecar has checked every value written here).
		priv, _ := wgKey(n.PrivateKey)
		peer, _ := wgKey(n.PeerPublicKey)
		var b strings.Builder
		b.WriteString("[Interface]\n")
		b.WriteString("PrivateKey = " + priv + "\n")
		b.WriteString("Address = " + strings.Join(n.LocalAddress, ", ") + "\n")
		b.WriteString("DNS = 1.1.1.1\n")
		if n.MTU > 0 {
			b.WriteString("MTU = " + strconv.Itoa(n.MTU) + "\n")
		}
		a := n.AWG
		for _, kv := range [][2]string{{"Jc", strconv.Itoa(a.Jc)}, {"Jmin", strconv.Itoa(a.Jmin)}, {"Jmax", strconv.Itoa(a.Jmax)},
			{"S1", strconv.Itoa(a.S1)}, {"S2", strconv.Itoa(a.S2)}, {"H1", a.H1}, {"H2", a.H2}, {"H3", a.H3}, {"H4", a.H4}} {
			if kv[1] != "" && kv[1] != "0" {
				b.WriteString(kv[0] + " = " + kv[1] + "\n")
			}
		}
		b.WriteString("\n[Peer]\n")
		b.WriteString("PublicKey = " + peer + "\n")
		if psk, ok := wgKey(n.PreSharedKey); ok {
			b.WriteString("PresharedKey = " + psk + "\n")
		}
		b.WriteString("Endpoint = " + n.hostPort() + "\n")
		b.WriteString("AllowedIPs = 0.0.0.0/0, ::/0\n")
		b.WriteString("PersistentKeepalive = 25\n")
		b.WriteString("\n[Socks5]\nBindAddress = " + bind + "\n")
		if pass != "" {
			b.WriteString("Username = " + SocksUser + "\nPassword = " + pass + "\n")
		}
		files["wireproxy.conf"] = b.String()
		args = []string{"-c", dir + sep + "wireproxy.conf"}
	}
	return args, env, files
}

// urlUser escapes a user name or password for the user part of a URL.
func urlUser(s string) string {
	const keep = "-._~"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte(keep, c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// parseNaive reads naive+https://user:pass@host:port#name (the proxy URL
// NaiveProxy takes, with the scheme marked) or naive://...?proto=quic.
func parseNaive(link, scheme string) (*Node, error) {
	u, err := splitURI(link)
	if err != nil {
		return nil, err
	}
	n := &Node{Proto: "naive", Name: cleanName(u.fragment), Server: u.host, Port: atoi(u.port), Transport: "https"}
	if n.Port == 0 {
		n.Port = 443
	}
	if strings.HasSuffix(scheme, "+quic") || u.query["proto"] == "quic" {
		n.Transport = "quic"
	}
	n.Username, n.Password, _ = strings.Cut(unescape(u.user), ":")
	n.SNI = u.query["sni"]
	return n, nil
}

// parseJuicity reads juicity://uuid:password@host:port?congestion_control=bbr&sni=&allow_insecure=&pinned_certchain_sha256=#name.
func parseJuicity(link string) (*Node, error) {
	u, err := splitURI(link)
	if err != nil {
		return nil, err
	}
	q := u.query
	n := &Node{Proto: "juicity", Name: cleanName(u.fragment), Server: u.host, Port: atoi(u.port),
		SNI: q["sni"], Insecure: truthy(q["allow_insecure"]), CongestionControl: q["congestion_control"], PinnedCert: q["pinned_certchain_sha256"]}
	n.UUID, n.Password, _ = strings.Cut(unescape(u.user), ":")
	return n, nil
}

// parseMieru reads mieru://user:password@host:port?protocol=tcp|udp&mtu=1400#name.
func parseMieru(link string) (*Node, error) {
	u, err := splitURI(link)
	if err != nil {
		return nil, err
	}
	n := &Node{Proto: "mieru", Name: cleanName(u.fragment), Server: u.host, Port: atoi(u.port),
		Transport: strings.ToLower(firstNonEmpty(u.query["protocol"], u.query["proto"], "tcp")), MTU: atoi(u.query["mtu"])}
	n.Username, n.Password, _ = strings.Cut(unescape(u.user), ":")
	return n, nil
}

// parseBrook reads Brook's own links: brook://server?server=host:port&password=...,
// brook://wsserver?wsserver=ws://host:port/path&password=... and
// brook://wssserver?wssserver=wss://host:port/path&password=...&address=host:port&insecure=true.
func parseBrook(link string) (*Node, error) {
	rest := link[len("brook://"):]
	kind, qs, _ := strings.Cut(rest, "?")
	q := parseQuery(qs)
	n := &Node{Proto: "brook", Name: cleanName(firstNonEmpty(q["name"], kind)), Password: q["password"]}
	setHP := func(hp string) error {
		h, p, err := net.SplitHostPort(hp)
		if err != nil {
			return fmt.Errorf("bad brook address %q", hp)
		}
		n.Server, n.Port = h, atoi(p)
		return nil
	}
	switch kind {
	case "server":
		if err := setHP(q["server"]); err != nil {
			return nil, err
		}
	case "wsserver", "wssserver":
		raw := q[kind]
		scheme, after, ok := strings.Cut(raw, "://")
		if !ok {
			return nil, fmt.Errorf("bad brook %s", kind)
		}
		hp, path, _ := strings.Cut(after, "/")
		if !strings.Contains(hp, ":") {
			hp += map[string]string{"ws": ":80", "wss": ":443"}[scheme]
		}
		if err := setHP(hp); err != nil {
			return nil, err
		}
		n.Transport = scheme
		if path != "" {
			n.Path = "/" + path
		}
		if a := q["address"]; a != "" {
			// The name is for TLS; the connection goes to the address.
			n.SNI = n.Server
			if err := setHP(a); err != nil {
				return nil, err
			}
		}
		n.Insecure = truthy(q["insecure"])
	default:
		return nil, ErrUnsupported
	}
	return n, nil
}

// parseWStunnel reads wstunnel://host:port?path=<secret prefix>&sni=&insecure=#name.
func parseWStunnel(link string) (*Node, error) {
	u, err := splitURI(link)
	if err != nil {
		return nil, err
	}
	n := &Node{Proto: "wstunnel", Name: cleanName(u.fragment), Server: u.host, Port: atoi(u.port),
		Path: firstNonEmpty(u.query["path"], u.query["prefix"], unescape(u.user)), SNI: u.query["sni"], Insecure: truthy(u.query["insecure"])}
	if n.Port == 0 {
		n.Port = 443
	}
	return n, nil
}

// parseAmneziaWG reads a WireGuard link with AmneziaWG's parameters:
// amneziawg://privatekey@host:port?publickey=&address=&psk=&mtu=&jc=&jmin=&jmax=&s1=&s2=&h1=&h2=&h3=&h4=#name.
func parseAmneziaWG(link string) (*Node, error) {
	n, err := parseWireGuard(link)
	if err != nil {
		return nil, err
	}
	u, _ := splitURI(link)
	q := u.query
	n.Proto = "amneziawg"
	n.AWG = &AmneziaWG{Jc: atoi(q["jc"]), Jmin: atoi(q["jmin"]), Jmax: atoi(q["jmax"]), S1: atoi(q["s1"]), S2: atoi(q["s2"]),
		H1: q["h1"], H2: q["h2"], H3: q["h3"], H4: q["h4"]}
	return n, nil
}
