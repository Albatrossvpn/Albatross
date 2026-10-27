package node

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

var schemeRe = regexp.MustCompile(`(?i)(vmess|vless|trojan|trojan-go|ssr?|ssh|hysteria2|hy2|hysteria|tuic|wireguard|wg|amneziawg|awg|anytls|socks5?|socks4|naive(?:\+https|\+quic)?|juicity|mieru|brook|wstunnel)://`)

// ErrUnsupported marks links that parse but cannot be run by any core.
var ErrUnsupported = errors.New("unsupported")

// ExtractLinks finds share links in any text: plain lists, base64
// subscriptions, HTML pages (Telegram previews) or mixed content.
func ExtractLinks(text string) []string {
	if looksHTML(text) {
		text = stripHTML(text)
	}
	if !schemeRe.MatchString(head(text, 4096)) {
		if dec, ok := decodeSubscription(text); ok {
			text = dec
		}
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if line == "" {
			continue
		}
		locs := schemeRe.FindAllStringIndex(line, -1)
		for i, loc := range locs {
			// A scheme inside another link's fragment (e.g. "#vless://...")
			// still starts a new link only when separated by whitespace.
			if loc[0] > 0 && !isSpace(line[loc[0]-1]) && i > 0 {
				continue
			}
			end := len(line)
			for _, next := range locs[i+1:] {
				if next[0] > 0 && isSpace(line[next[0]-1]) {
					end = next[0]
					break
				}
			}
			link := strings.TrimSpace(line[loc[0]:end])
			link = strings.TrimRight(link, ",;")
			if len(link) > 12 {
				out = append(out, link)
			}
		}
	}
	return out
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\r' }

func head(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func looksHTML(s string) bool {
	h := strings.ToLower(head(s, 4096))
	return strings.Contains(h, "<html") || strings.Contains(h, "<!doctype") || strings.Contains(h, "<div") || strings.Contains(h, "<body")
}

var tagRe = regexp.MustCompile(`(?s)<br\s*/?>|</?(div|p|code|pre|span|a|b|i|tr|td|li|blockquote|section)[^>]*>|<[^>]+>`)

func stripHTML(s string) string {
	s = tagRe.ReplaceAllStringFunc(s, func(t string) string {
		lt := strings.ToLower(t)
		if strings.HasPrefix(lt, "<br") || strings.HasPrefix(lt, "</div") || strings.HasPrefix(lt, "</p") ||
			strings.HasPrefix(lt, "<div") || strings.HasPrefix(lt, "<p") || strings.HasPrefix(lt, "</code") ||
			strings.HasPrefix(lt, "<code") || strings.HasPrefix(lt, "</pre") || strings.HasPrefix(lt, "</li") ||
			strings.HasPrefix(lt, "</blockquote") || strings.HasPrefix(lt, "</td") {
			return "\n"
		}
		return ""
	})
	return html.UnescapeString(s)
}

// decodeSubscription decodes a whole-body base64 subscription.
func decodeSubscription(text string) (string, bool) {
	compact := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, text)
	if len(compact) < 8 {
		return "", false
	}
	b, err := decodeB64(compact)
	if err != nil {
		return "", false
	}
	s := string(b)
	if schemeRe.MatchString(head(s, 8192)) || strings.Contains(head(s, 8192), "proxies:") {
		return s, true
	}
	return "", false
}

// trimB64 cuts trailing junk glued after a base64 body, such as the next
// link's scheme in "...In0=vless://".
func trimB64(s string) string {
	s = strings.TrimSpace(s)
	end := 0
	for end < len(s) {
		c := s[end]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '/' || c == '-' || c == '_' {
			end++
			continue
		}
		break
	}
	for end < len(s) && s[end] == '=' {
		end++
	}
	return s[:end]
}

// decodeB64 accepts standard or URL alphabets, padded or not.
func decodeB64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, "=")
	if strings.ContainsAny(s, "-_") {
		return base64.RawURLEncoding.DecodeString(s)
	}
	return base64.RawStdEncoding.DecodeString(s)
}

// ParseLink parses one share link.
func ParseLink(link string) (*Node, error) {
	link = strings.TrimSpace(link)
	i := strings.Index(link, "://")
	if i <= 0 {
		return nil, fmt.Errorf("not a link")
	}
	scheme := strings.ToLower(link[:i])
	var n *Node
	var err error
	switch scheme {
	case "vmess":
		n, err = parseVMess(link)
	case "vless", "trojan":
		n, err = parseStandard(link, scheme)
	case "trojan-go":
		// Trojan-Go is Trojan, usually over WebSocket: the same link
		// form, which the cores run as a trojan outbound.
		n, err = parseStandard("trojan://"+link[len("trojan-go://"):], "trojan")
	case "naive", "naive+https", "naive+quic":
		n, err = parseNaive(link, scheme)
	case "juicity":
		n, err = parseJuicity(link)
	case "mieru":
		n, err = parseMieru(link)
	case "brook":
		n, err = parseBrook(link)
	case "wstunnel":
		n, err = parseWStunnel(link)
	case "amneziawg", "awg":
		n, err = parseAmneziaWG(link)
	case "ss":
		n, err = parseSS(link)
	case "hysteria2", "hy2":
		n, err = parseHy2(link)
	case "hysteria":
		n, err = parseHysteria(link)
	case "tuic":
		n, err = parseTUIC(link)
	case "anytls":
		n, err = parseAnyTLS(link)
	case "wireguard", "wg":
		n, err = parseWireGuard(link)
	case "ssh":
		n, err = parseSSH(link)
	case "socks", "socks5":
		n, err = parseSocks(link)
	case "albatross":
		return parseInternal(link)
	case "ssr", "socks4":
		return nil, ErrUnsupported
	default:
		return nil, ErrUnsupported
	}
	if err != nil {
		return nil, err
	}
	n.Source = link
	n.normalize()
	if err := n.Validate(); err != nil {
		return nil, err
	}
	return n, nil
}

// uri is a lenient split of scheme://userinfo@host:port/path?query#fragment.
type uri struct {
	user     string
	host     string
	port     string
	path     string
	query    map[string]string
	fragment string
}

func splitURI(link string) (*uri, error) {
	rest := link[strings.Index(link, "://")+3:]
	u := &uri{query: map[string]string{}}
	if i := strings.Index(rest, "#"); i >= 0 {
		u.fragment = unescape(rest[i+1:])
		rest = rest[:i]
	}
	if i := strings.Index(rest, "?"); i >= 0 {
		u.query = parseQuery(rest[i+1:])
		rest = rest[:i]
	}
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		u.user = rest[:i]
		rest = rest[i+1:]
	}
	if i := strings.Index(rest, "/"); i >= 0 {
		u.path = rest[i:]
		rest = rest[:i]
	}
	host, port, err := splitHostPort(rest)
	if err != nil {
		return nil, err
	}
	u.host, u.port = host, port
	return u, nil
}

func splitHostPort(s string) (string, string, error) {
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end < 0 {
			return "", "", fmt.Errorf("bad ipv6 host")
		}
		host := s[1:end]
		port := strings.TrimPrefix(s[end+1:], ":")
		return host, port, nil
	}
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return s, "", nil
	}
	if strings.Count(s, ":") > 1 && net.ParseIP(s) != nil {
		return s, "", nil
	}
	return s[:i], s[i+1:], nil
}

func parseQuery(q string) map[string]string {
	out := map[string]string{}
	for _, kv := range strings.Split(q, "&") {
		if kv == "" {
			continue
		}
		k, v, _ := strings.Cut(kv, "=")
		k = strings.ToLower(strings.TrimSpace(unescape(k)))
		for strings.HasPrefix(k, "amp;") {
			k = k[4:]
		}
		if k == "" {
			continue
		}
		if _, dup := out[k]; dup {
			continue
		}
		out[k] = strings.TrimSpace(unescape(v))
	}
	return out
}

func unescape(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	if v, err := url.PathUnescape(s); err == nil {
		return v
	}
	return s
}

func atoi(s string) int {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, ",-/ "); i > 0 {
		s = s[:i]
	}
	v, _ := strconv.Atoi(s)
	return v
}

func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || arabicScript(r) {
			return -1
		}
		return r
	}, s)
	// Dropping words can leave runs of spaces and stray separators.
	s = strings.Join(strings.Fields(s), " ")
	s = strings.Trim(s, " -_|:.,()[]")
	if utf8.RuneCountInString(s) > 80 {
		r := []rune(s)
		s = string(r[:80])
	}
	return s
}

// applyStreamParams reads the Xray-standard transport and TLS query
// parameters shared by vless, trojan and URL-form vmess.
func (n *Node) applyStreamParams(q map[string]string) {
	n.Transport = q["type"]
	n.HeaderType = q["headertype"]
	n.Host = q["host"]
	n.Path = q["path"]
	n.ServiceName = q["servicename"]
	n.Mode = q["mode"]
	n.Extra = q["extra"]
	n.Seed = q["seed"]
	if n.Transport == "grpc" && n.ServiceName == "" {
		n.ServiceName = strings.TrimPrefix(n.Path, "/")
	}
	if n.Transport == "grpc" && n.Host == "" {
		n.Host = q["authority"]
	}
	if ed := atoi(q["ed"]); ed > 0 {
		n.EarlyData = ed
	}
	n.Security = q["security"]
	n.SNI = firstNonEmpty(q["sni"], q["peer"], q["servername"])
	n.ALPN = splitList(q["alpn"])
	n.Fingerprint = q["fp"]
	n.Insecure = truthy(q["allowinsecure"]) || truthy(q["insecure"]) || truthy(q["allow_insecure"]) || truthy(q["skip-cert-verify"])
	n.PublicKey = firstNonEmpty(q["pbk"], q["publickey"])
	n.ShortID = q["sid"]
	n.SpiderX = q["spx"]
	n.ECH = q["ech"]
	n.PinnedCert = q["pcs"]
	n.VerifyName = q["vcn"]
	n.Flow = q["flow"]
	n.FinalMask = q["fm"]
	if strings.Contains(n.FinalMask, "fragment") || q["fragment"] != "" {
		n.Fragment = true
	}
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func parseStandard(link, scheme string) (*Node, error) {
	u, err := splitURI(link)
	if err != nil {
		return nil, err
	}
	n := &Node{Name: cleanName(u.fragment), Server: u.host, Port: atoi(u.port)}
	n.applyStreamParams(u.query)
	if scheme == "vless" {
		n.Proto = "vless"
		n.UUID = unescape(u.user)
		n.Encryption = u.query["encryption"]
	} else {
		n.Proto = "trojan"
		n.Password = unescape(u.user)
		if n.Security == "" {
			n.Security = "tls"
		}
	}
	return n, nil
}

// vmessJSON is the v2rayN share format. Numbers arrive as strings or numbers.
type vmessJSON struct {
	FM          json.RawMessage `json:"fm"` // Xray finalmask (own servers)
	PS          any             `json:"ps"`
	Add         any             `json:"add"`
	Port        any             `json:"port"`
	ID          any             `json:"id"`
	Aid         any             `json:"aid"`
	Scy         any             `json:"scy"`
	Net         any             `json:"net"`
	Type        any             `json:"type"`
	Host        any             `json:"host"`
	Path        any             `json:"path"`
	TLS         any             `json:"tls"`
	SNI         any             `json:"sni"`
	ALPN        any             `json:"alpn"`
	FP          any             `json:"fp"`
	Insecure    any             `json:"insecure"`
	AllowInsec  any             `json:"allowInsecure"`
	SkipVerify  any             `json:"skip-cert-verify"`
	ServiceName any             `json:"serviceName"`
	Mode        any             `json:"mode"`
	PBK         any             `json:"pbk"`
	SID         any             `json:"sid"`
}

func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "true"
		}
		return "false"
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func parseVMess(link string) (*Node, error) {
	body := link[len("vmess://"):]
	name := ""
	if i := strings.Index(body, "#"); i >= 0 {
		name = unescape(body[i+1:])
		body = body[:i]
	}
	if strings.Contains(body, "@") {
		// Xray's URL form: vmess://uuid@host:port?type=ws&security=tls...
		n, err := parseStandard("vless://"+body, "vless")
		if err != nil {
			return nil, err
		}
		n.Proto = "vmess"
		n.Encryption = ""
		n.Method = "auto"
		n.Name = cleanName(name)
		return n, nil
	}
	if q := strings.Index(body, "?"); q > 0 {
		// Shadowrocket form: base64(security:uuid@host:port)?remarks=...&obfs=websocket
		if dec, err := decodeB64(trimB64(body[:q])); err == nil && strings.Contains(string(dec), "@") && !strings.HasPrefix(string(dec), "{") {
			return parseShadowrocketVMess(string(dec), parseQuery(body[q+1:]), name)
		}
	}
	raw, err := decodeB64(trimB64(body))
	if err != nil {
		return nil, fmt.Errorf("vmess base64: %w", err)
	}
	var j vmessJSON
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("vmess json: %w", err)
	}
	n := &Node{
		Proto:       "vmess",
		Name:        cleanName(firstNonEmpty(str(j.PS), name)),
		Server:      str(j.Add),
		Port:        atoi(str(j.Port)),
		UUID:        str(j.ID),
		AlterID:     atoi(str(j.Aid)),
		Method:      str(j.Scy),
		Transport:   strings.ToLower(str(j.Net)),
		HeaderType:  str(j.Type),
		Host:        str(j.Host),
		Path:        str(j.Path),
		SNI:         str(j.SNI),
		ALPN:        splitList(str(j.ALPN)),
		Fingerprint: str(j.FP),
		Insecure:    truthy(str(j.Insecure)) || truthy(str(j.AllowInsec)) || truthy(str(j.SkipVerify)),
		Mode:        str(j.Mode),
		PublicKey:   str(j.PBK),
		ShortID:     str(j.SID),
	}
	switch strings.ToLower(str(j.TLS)) {
	case "tls", "true", "1":
		n.Security = "tls"
	case "reality":
		n.Security = "reality"
	}
	if n.Transport == "grpc" {
		n.ServiceName = firstNonEmpty(str(j.ServiceName), strings.TrimPrefix(n.Path, "/"))
	}
	if n.Transport == "kcp" || n.Transport == "mkcp" {
		n.Seed = n.Path
	}
	if len(j.FM) > 0 && j.FM[0] == '{' {
		n.FinalMask = string(j.FM)
	}
	return n, nil
}

func parseShadowrocketVMess(dec string, q map[string]string, name string) (*Node, error) {
	at := strings.LastIndex(dec, "@")
	sec, id, _ := strings.Cut(dec[:at], ":")
	host, port, err := splitHostPort(dec[at+1:])
	if err != nil {
		return nil, err
	}
	n := &Node{
		Proto:   "vmess",
		Name:    cleanName(firstNonEmpty(q["remarks"], name)),
		Server:  host,
		Port:    atoi(port),
		UUID:    id,
		Method:  sec,
		AlterID: atoi(q["alterid"]),
		Path:    q["path"],
		Host:    q["obfsparam"],
		SNI:     q["peer"],
	}
	switch q["obfs"] {
	case "websocket", "ws":
		n.Transport = "ws"
	case "http":
		n.Transport, n.HeaderType = "tcp", "http"
	case "grpc":
		n.Transport = "grpc"
		n.ServiceName = q["path"]
	default:
		n.Transport = "tcp"
	}
	if truthy(q["tls"]) {
		n.Security = "tls"
		n.Insecure = truthy(q["allowinsecure"])
	}
	return n, nil
}

// ssCredential says whether a link's userinfo is a Shadowsocks
// "method:password", plain or base64, with a method Shadowsocks has.
func ssCredential(userinfo string) bool {
	cred := unescape(userinfo)
	if dec, err := decodeB64(cred); err == nil && utf8.Valid(dec) && strings.Contains(string(dec), ":") {
		cred = string(dec)
	}
	method, _, ok := strings.Cut(cred, ":")
	if !ok {
		return false
	}
	method = strings.ToLower(method)
	if method == "chacha20-poly1305" {
		method = "chacha20-ietf-poly1305"
	}
	return ssMethods[method]
}

func parseSS(link string) (*Node, error) {
	body := link[len("ss://"):]
	name := ""
	if i := strings.Index(body, "#"); i >= 0 {
		name = unescape(body[i+1:])
		body = body[:i]
	}
	query := map[string]string{}
	if i := strings.Index(body, "?"); i >= 0 {
		query = parseQuery(body[i+1:])
		body = body[:i]
	}
	body = strings.TrimSuffix(body, "/")
	// Mislabelled links: a v2rayN vmess JSON or a vless URL behind "ss://".
	if !strings.Contains(body, "@") {
		if dec, err := decodeB64(trimB64(body)); err == nil && strings.HasPrefix(strings.TrimSpace(string(dec)), "{") {
			return parseVMess("vmess://" + trimB64(body) + "#" + url.PathEscape(name))
		}
	} else if at := strings.LastIndex(body, "@"); query["encryption"] != "" || (isUUID(body[:at]) && (query["type"] != "" || query["security"] != "")) {
		return parseStandard("vless://"+link[len("ss://"):], "vless")
	} else if (query["type"] != "" || query["security"] != "") && !ssCredential(body[:strings.LastIndex(body, "@")]) {
		// Panels such as 3x-ui add type= to genuine Shadowsocks links, so
		// only a userinfo that is not "method:password" means Trojan.
		return parseStandard("trojan://"+link[len("ss://"):], "trojan")
	}
	var method, password, hostport string
	if at := strings.LastIndex(body, "@"); at >= 0 {
		userinfo, hp := body[:at], body[at+1:]
		hostport = strings.TrimSuffix(hp, "/")
		if dec, err := decodeB64(unescape(userinfo)); err == nil && strings.Contains(string(dec), ":") && utf8.Valid(dec) {
			method, password, _ = strings.Cut(string(dec), ":")
		} else {
			method, password, _ = strings.Cut(unescape(userinfo), ":")
		}
	} else {
		dec, err := decodeB64(unescape(body))
		if err != nil {
			return nil, fmt.Errorf("ss base64: %w", err)
		}
		s := string(dec)
		at := strings.LastIndex(s, "@")
		if at < 0 {
			return nil, fmt.Errorf("ss: no server")
		}
		method, password, _ = strings.Cut(s[:at], ":")
		hostport = s[at+1:]
	}
	host, port, err := splitHostPort(hostport)
	if err != nil {
		return nil, err
	}
	n := &Node{
		Proto:    "shadowsocks",
		Name:     cleanName(name),
		Server:   host,
		Port:     atoi(port),
		Method:   strings.ToLower(strings.TrimSpace(method)),
		Password: password,
	}
	if p := query["plugin"]; p != "" {
		parts := strings.SplitN(p, ";", 2)
		n.Plugin = parts[0]
		if len(parts) == 2 {
			n.PluginOpts = parts[1]
		}
	}
	return n, nil
}

func parseHy2(link string) (*Node, error) {
	u, err := splitURI(link)
	if err != nil {
		return nil, err
	}
	q := u.query
	n := &Node{
		Proto:        "hysteria2",
		Name:         cleanName(u.fragment),
		Server:       u.host,
		Password:     unescape(u.user),
		Security:     "tls",
		SNI:          firstNonEmpty(q["sni"], q["peer"]),
		Insecure:     truthy(q["insecure"]) || truthy(q["allowinsecure"]) || truthy(q["allow_insecure"]) || truthy(q["skip-cert-verify"]),
		ALPN:         splitList(q["alpn"]),
		Obfs:         q["obfs"],
		ObfsPassword: q["obfs-password"],
		UpMbps:       atoi(firstNonEmpty(q["upmbps"], q["up-mbps"], q["up"])),
		DownMbps:     atoi(firstNonEmpty(q["downmbps"], q["down-mbps"], q["down"])),
	}
	if n.Obfs == "none" {
		n.Obfs = ""
	}
	if q["pinsha256"] != "" {
		// The pin replaces CA validation; sing-box cannot pin a leaf
		// certificate hash, so fall back to skipping verification.
		n.Insecure = true
	}
	setPorts(n, u.port, q["mport"])
	return n, nil
}

// setPorts handles "443", "20000-30000" and "443,20000-30000".
func setPorts(n *Node, port, mport string) {
	spec := port
	if mport != "" {
		spec = mport
	}
	if strings.ContainsAny(spec, ",-") {
		n.PortHop = spec
		n.Port = atoi(spec)
		if p := atoi(port); p > 0 {
			n.Port = p
		}
		return
	}
	n.Port = atoi(port)
}

func parseHysteria(link string) (*Node, error) {
	u, err := splitURI(link)
	if err != nil {
		return nil, err
	}
	q := u.query
	if p := q["protocol"]; p != "" && p != "udp" {
		return nil, ErrUnsupported
	}
	n := &Node{
		Proto:        "hysteria",
		Name:         cleanName(u.fragment),
		Server:       u.host,
		Password:     firstNonEmpty(q["auth"], q["auth_str"], unescape(u.user)),
		Security:     "tls",
		SNI:          firstNonEmpty(q["peer"], q["sni"]),
		Insecure:     truthy(q["insecure"]) || truthy(q["allowinsecure"]),
		ALPN:         splitList(q["alpn"]),
		ObfsPassword: firstNonEmpty(q["obfsparam"], q["obfs-password"]),
		UpMbps:       atoi(firstNonEmpty(q["upmbps"], q["up"])),
		DownMbps:     atoi(firstNonEmpty(q["downmbps"], q["down"])),
	}
	if o := q["obfs"]; o != "" && o != "none" && o != "xplus" && n.ObfsPassword == "" {
		// Some links put the xplus password directly in obfs.
		n.ObfsPassword = o
	}
	setPorts(n, u.port, q["mport"])
	return n, nil
}

func parseTUIC(link string) (*Node, error) {
	u, err := splitURI(link)
	if err != nil {
		return nil, err
	}
	q := u.query
	id, pw, _ := strings.Cut(unescape(u.user), ":")
	n := &Node{
		Proto:             "tuic",
		Name:              cleanName(u.fragment),
		Server:            u.host,
		Port:              atoi(u.port),
		UUID:              id,
		Password:          pw,
		Security:          "tls",
		SNI:               q["sni"],
		Insecure:          truthy(q["allow_insecure"]) || truthy(q["insecure"]) || truthy(q["allowinsecure"]),
		ALPN:              splitList(q["alpn"]),
		CongestionControl: firstNonEmpty(q["congestion_control"], q["congestion_controller"]),
		UDPRelayMode:      q["udp_relay_mode"],
		DisableSNI:        truthy(q["disable_sni"]),
	}
	return n, nil
}

func parseAnyTLS(link string) (*Node, error) {
	u, err := splitURI(link)
	if err != nil {
		return nil, err
	}
	q := u.query
	return &Node{
		Proto:       "anytls",
		Name:        cleanName(u.fragment),
		Server:      u.host,
		Port:        atoi(u.port),
		Password:    unescape(u.user),
		Security:    "tls",
		SNI:         q["sni"],
		Insecure:    truthy(q["insecure"]) || truthy(q["allowinsecure"]),
		ALPN:        splitList(q["alpn"]),
		Fingerprint: q["fp"],
	}, nil
}

func parseWireGuard(link string) (*Node, error) {
	u, err := splitURI(link)
	if err != nil {
		return nil, err
	}
	q := u.query
	n := &Node{
		Proto:         "wireguard",
		Name:          cleanName(u.fragment),
		Server:        u.host,
		Port:          atoi(u.port),
		PrivateKey:    firstNonEmpty(unescape(u.user), q["privatekey"], q["secretkey"]),
		PeerPublicKey: firstNonEmpty(q["publickey"], q["peerpublickey"], q["public_key"]),
		PreSharedKey:  firstNonEmpty(q["presharedkey"], q["psk"]),
		MTU:           atoi(q["mtu"]),
	}
	n.LocalAddress = append(n.LocalAddress, splitList(firstNonEmpty(q["address"], q["ip"], q["addresses"]))...)
	if r := q["reserved"]; r != "" {
		n.Reserved = parseReserved(r)
	}
	return n, nil
}

func parseReserved(s string) []int {
	if strings.Contains(s, ",") {
		var out []int
		for _, p := range splitList(s) {
			v, err := strconv.Atoi(p)
			if err != nil || v < 0 || v > 255 {
				return nil
			}
			out = append(out, v)
		}
		if len(out) == 3 {
			return out
		}
		return nil
	}
	if b, err := decodeB64(s); err == nil && len(b) == 3 {
		return []int{int(b[0]), int(b[1]), int(b[2])}
	}
	return nil
}

func parseSocks(link string) (*Node, error) {
	u, err := splitURI(link)
	if err != nil {
		return nil, err
	}
	n := &Node{Proto: "socks", Name: cleanName(u.fragment), Server: u.host, Port: atoi(u.port)}
	if u.user != "" {
		user := unescape(u.user)
		if !strings.Contains(user, ":") {
			if dec, err := decodeB64(user); err == nil {
				user = string(dec)
			}
		}
		n.Username, n.Password, _ = strings.Cut(user, ":")
	}
	return n, nil
}

var fingerprints = map[string]bool{
	"chrome": true, "firefox": true, "edge": true, "safari": true, "360": true, "qq": true,
	"ios": true, "android": true, "random": true, "randomized": true,
}

// normalize fixes common inconsistencies so the two cores agree on a node.
func (n *Node) normalize() {
	n.Server = strings.Trim(strings.TrimSpace(n.Server), "[]")
	if n.Sidecar() != "" {
		// The helpers read the fields as their links name them; the
		// cores' transports and TLS settings do not apply.
		n.Transport = strings.ToLower(strings.TrimSpace(n.Transport))
		n.Fingerprint = ""
		return
	}
	n.Transport = strings.ToLower(strings.TrimSpace(n.Transport))
	switch n.Transport {
	case "raw", "none":
		n.Transport = "tcp"
	case "h2":
		n.Transport = "http"
	case "splithttp":
		n.Transport = "xhttp"
	case "mkcp":
		n.Transport = "kcp"
	case "gun":
		n.Transport = "grpc"
	}
	if n.Proto == "vless" || n.Proto == "vmess" || n.Proto == "trojan" {
		if n.Transport == "" {
			n.Transport = "tcp"
		}
	} else if n.Proto != "shadowsocks" {
		n.Transport = ""
	}
	n.HeaderType = strings.ToLower(n.HeaderType)
	if n.HeaderType != "http" {
		if n.Transport != "kcp" {
			n.HeaderType = ""
		}
	}
	n.Security = strings.ToLower(n.Security)
	switch n.Security {
	case "tls", "reality":
	case "xtls":
		n.Security = "tls"
	default:
		n.Security = ""
	}
	if n.Security == "" {
		n.SNI, n.ALPN, n.Fingerprint, n.PublicKey, n.ShortID = "", nil, "", "", ""
		n.Insecure = false
	}
	n.Fingerprint = strings.ToLower(n.Fingerprint)
	if n.Fingerprint != "" && !fingerprints[n.Fingerprint] {
		n.Fingerprint = "chrome"
	}
	if n.Security == "reality" && n.Fingerprint == "" {
		n.Fingerprint = "chrome"
	}
	n.Flow = strings.ToLower(n.Flow)
	if n.Flow == "none" {
		n.Flow = ""
	}
	if n.Flow != "" && (n.Transport != "tcp" || n.Security == "") {
		n.Flow = ""
	}
	n.Encryption = strings.TrimSpace(n.Encryption)
	if n.Proto == "vless" && !strings.HasPrefix(n.Encryption, "mlkem768x25519plus.") {
		n.Encryption = "none"
	}
	if n.Proto == "vmess" {
		n.Method = strings.ToLower(n.Method)
		switch n.Method {
		case "auto", "none", "zero", "aes-128-gcm", "chacha20-poly1305":
		default:
			n.Method = "auto"
		}
		if n.AlterID < 0 || n.AlterID > 65535 {
			n.AlterID = 0
		}
	}
	if n.Proto == "shadowsocks" {
		switch n.Plugin {
		case "simple-obfs", "obfs":
			n.Plugin = "obfs-local"
		}
		if n.Method == "chacha20-poly1305" {
			n.Method = "chacha20-ietf-poly1305"
		}
	}
	if n.Path != "" && !strings.HasPrefix(n.Path, "/") && (n.Transport == "ws" || n.Transport == "httpupgrade" || n.Transport == "http" || n.Transport == "xhttp") {
		n.Path = "/" + n.Path
	}
	if n.Transport == "ws" || n.Transport == "httpupgrade" {
		n.Path, n.EarlyData = splitEarlyData(n.Path, n.EarlyData)
	}
	if strings.Contains(n.Path, "%") {
		n.Path = badEscapeRe.ReplaceAllStringFunc(n.Path, func(m string) string { return "%25" + m[1:] })
	}
	if (n.Proto == "vmess" || n.Proto == "vless" || n.Proto == "tuic") && n.UUID != "" && !isUUID(n.UUID) {
		n.UUID = uuidV5(n.UUID)
	}
	if n.Proto == "wireguard" {
		for i, a := range n.LocalAddress {
			if !strings.Contains(a, "/") {
				if strings.Contains(a, ":") {
					n.LocalAddress[i] = a + "/128"
				} else {
					n.LocalAddress[i] = a + "/32"
				}
			}
		}
	}
	n.Name = cleanName(n.Name)
	if n.Name == "" {
		n.Name = strings.ToUpper(n.Proto) + " " + n.Server
	}
}

// arabicScript reports Persian and Arabic letters, digits and joiners. The
// app is English only, so publishers' Persian names are not shown; the
// rest of the name (flags, countries, numbers) stays.
func arabicScript(r rune) bool {
	return r >= 0x0600 && r <= 0x06FF || r >= 0x0750 && r <= 0x077F || r >= 0x08A0 && r <= 0x08FF ||
		r >= 0xFB50 && r <= 0xFDFF || r >= 0xFE70 && r <= 0xFEFF || r == 0x200C || r == 0x200F
}

var badEscapeRe = regexp.MustCompile(`%(?:[^0-9A-Fa-f]|[0-9A-Fa-f][^0-9A-Fa-f]|[0-9A-Fa-f]?$)`)

// splitEarlyData moves Xray's "?ed=2048" path suffix into EarlyData.
func splitEarlyData(path string, ed int) (string, int) {
	i := strings.Index(path, "?")
	if i < 0 {
		return path, ed
	}
	q := parseQuery(path[i+1:])
	if v := atoi(q["ed"]); v > 0 {
		ed = v
		delete(q, "ed")
		path = path[:i]
		if len(q) > 0 {
			var parts []string
			for k, v := range q {
				parts = append(parts, k+"="+v)
			}
			sort.Strings(parts)
			path += "?" + strings.Join(parts, "&")
		}
	}
	return path, ed
}

// ParseText extracts every usable node from a subscription body in any of
// the supported formats. It also reports how many links it saw.
func ParseText(text string) (nodes []*Node, seen int) {
	seen = EachText(text, func(n *Node) { nodes = append(nodes, n) })
	return nodes, seen
}

// EachText is ParseText one node at a time: fn gets every usable node as
// it is parsed, so a list of a hundred thousand links never sits in memory
// as nodes all at once. It reports how many links it saw.
func EachText(text string, fn func(*Node)) (seen int) {
	var clash []*Node
	if strings.Contains(head(text, 1<<16), "proxies:") {
		clash = ParseClash(text)
	} else if !schemeRe.MatchString(head(text, 4096)) && !looksHTML(text) {
		if dec, ok := decodeSubscription(text); ok && strings.Contains(head(dec, 1<<16), "proxies:") {
			clash = ParseClash(dec)
		}
	}
	if len(clash) > 0 {
		for _, n := range clash {
			fn(n)
		}
		return len(clash)
	}
	if n := ParseOutlineJSON(text); n != nil {
		fn(n)
		return 1
	}
	links := ExtractLinks(text)
	for _, l := range links {
		if n, err := ParseLink(l); err == nil {
			fn(n)
		}
	}
	return len(links)
}

// Link returns a string that ParseLink turns back into this node: the
// original share link when there is one, otherwise an internal
// "albatross://" encoding (used for VPN Gate and generated WARP nodes).
func (n *Node) Link() string {
	if n.Source != "" && schemeRe.MatchString(head(n.Source, 16)) {
		return n.Source
	}
	b, _ := json.Marshal(n)
	return "albatross://" + base64.RawURLEncoding.EncodeToString(b)
}

func parseInternal(link string) (*Node, error) {
	b, err := base64.RawURLEncoding.DecodeString(link[len("albatross://"):])
	if err != nil {
		return nil, err
	}
	n := &Node{}
	if err := json.Unmarshal(b, n); err != nil {
		return nil, err
	}
	return n, nil
}

// WarpPlus makes a node for a warp-plus method.
func WarpPlus(mode, country string) *Node {
	n := &Node{Proto: "warpplus", WPMode: mode, WPCountry: strings.ToUpper(country)}
	switch mode {
	case "psiphon":
		n.Name = "Psiphon " + n.WPCountry
	case "gool":
		n.Name = "WARP in WARP"
	case "tor":
		n.Name = "Tor"
	case "masque":
		n.Name = "WARP over MASQUE"
	case "masque2":
		n.Name = "WARP over MASQUE (TCP)"
	default:
		n.Name = "Cloudflare WARP"
	}
	return n
}

// Usque reports whether the method runs on usque (WARP over MASQUE)
// rather than warp-plus.
func (n *Node) Usque() bool { return n.WPMode == "masque" || n.WPMode == "masque2" }

// UsqueConfig is the file usque keeps its device registration in.
func UsqueConfig(cacheDir string) string { return cacheDir + "/config.json" }

// UsqueRegisterArgs registers a WARP device (no account, Cloudflare's
// terms accepted) into cacheDir; run once before UsqueArgs.
func UsqueRegisterArgs(cacheDir string) []string {
	return []string{"-c", UsqueConfig(cacheDir), "register", "-a", "-n", "Albatross"}
}

// UsqueArgs runs usque as a SOCKS5 proxy on host:port over MASQUE: HTTP/3
// over QUIC, or for masque2 HTTP/2 over TCP, which gets through networks
// that throttle UDP.
func (n *Node) UsqueArgs(host, port, cacheDir string) []string {
	args := []string{"-c", UsqueConfig(cacheDir), "socks", "-b", host, "-p", port, "--always-reconnect"}
	if n.WPMode == "masque2" {
		args = append(args, "--http2")
	}
	return args
}

// WarpPlusArgs are the command-line flags that run this method.
func (n *Node) WarpPlusArgs(bind, cacheDir string) []string {
	args := []string{"-b", bind, "--cache-dir", cacheDir, "-4", "--scan"}
	switch n.WPMode {
	case "gool":
		args = append(args, "--gool")
	case "psiphon":
		args = append(args, "--cfon", "--country", n.WPCountry)
	}
	return args
}

// ParseOutlineJSON reads the answer of an Outline dynamic access key
// (ssconf://host/path, fetched over HTTPS): a JSON object naming one
// Shadowsocks server. Anything else gives nil.
func ParseOutlineJSON(text string) *Node {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "{") || len(t) > 1<<16 {
		return nil
	}
	var k struct {
		Server string `json:"server"`
		Port   int    `json:"server_port"`
		Pass   string `json:"password"`
		Method string `json:"method"`
		Prefix string `json:"prefix"`
		Name   string `json:"name"`
	}
	if err := json.Unmarshal([]byte(t), &k); err != nil || k.Server == "" || k.Port <= 0 || k.Method == "" {
		return nil
	}
	name := k.Name
	if name == "" {
		name = "Outline " + k.Server
	}
	n := &Node{Proto: "shadowsocks", Name: cleanName(name), Server: k.Server, Port: k.Port, Method: strings.ToLower(k.Method), Password: k.Pass}
	n.normalize()
	if n.Validate() != nil {
		return nil
	}
	return n
}

// parseSSH reads ssh://user:password@host:port?hk=<host key>#name, the
// form sing-box runs as an ssh outbound: the connection is a plain SSH
// session whose port forwarding carries the traffic. hk, optional, is the
// server's public host key ("ssh-ed25519 AAAA..."); without it any key is
// accepted, as with a first connection in a terminal.
func parseSSH(link string) (*Node, error) {
	u, err := splitURI(link)
	if err != nil {
		return nil, err
	}
	n := &Node{Proto: "ssh", Name: cleanName(u.fragment), Server: u.host, Port: atoi(u.port)}
	if n.Port == 0 {
		n.Port = 22
	}
	user := unescape(u.user)
	if !strings.Contains(user, ":") {
		if dec, err := decodeB64(user); err == nil && strings.Contains(string(dec), ":") {
			user = string(dec)
		}
	}
	n.Username, n.Password, _ = strings.Cut(user, ":")
	if n.Username == "" || n.Password == "" {
		return nil, fmt.Errorf("ssh link needs user:password")
	}
	n.HostKey = firstNonEmpty(u.query["hk"], u.query["host_key"], u.query["hostkey"])
	return n, nil
}

// Direct makes the serverless method: no server at all, the device's own
// connection with the TLS ClientHello fragmented, which opens sites that
// are blocked only by name (SNI) at full speed. It hides nothing from the
// network's operator, so the engine uses it only when no server works.
func Direct() *Node {
	return &Node{Proto: "direct", Name: "No server (TLS fragment)"}
}
