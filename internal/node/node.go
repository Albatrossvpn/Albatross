// Package node turns share links and subscription formats into a single
// normalized Node, and turns a Node into sing-box or Xray outbound configs.
package node

import (
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Engine is the core that can run a node.
type Engine int

const (
	EngineNone Engine = iota
	EngineSingBox
	EngineXray
	EngineWarpPlus
)

func (e Engine) String() string {
	switch e {
	case EngineSingBox:
		return "sing-box"
	case EngineXray:
		return "xray"
	case EngineWarpPlus:
		return "warp-plus"
	}
	return "none"
}

// Node is a protocol-independent description of one proxy server.
type Node struct {
	Proto   string `json:"proto"` // vmess vless trojan shadowsocks hysteria2 hysteria tuic anytls wireguard socks http openvpn
	Name    string `json:"name,omitempty"`
	Server  string `json:"server"`
	Port    int    `json:"port"`
	PortHop string `json:"port_hop,omitempty"` // "20000-30000,443" for hysteria port hopping

	UUID       string `json:"uuid,omitempty"`
	Password   string `json:"password,omitempty"`
	Username   string `json:"username,omitempty"`
	Method     string `json:"method,omitempty"` // shadowsocks cipher or vmess security
	AlterID    int    `json:"alter_id,omitempty"`
	Flow       string `json:"flow,omitempty"`
	Encryption string `json:"encryption,omitempty"` // vless encryption ("none" or post-quantum)

	Transport   string `json:"transport,omitempty"` // tcp ws grpc http httpupgrade xhttp kcp quic
	HeaderType  string `json:"header_type,omitempty"`
	Host        string `json:"host,omitempty"`
	Path        string `json:"path,omitempty"`
	ServiceName string `json:"service_name,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Extra       string `json:"extra,omitempty"` // xhttp extra JSON
	Seed        string `json:"seed,omitempty"`
	EarlyData   int    `json:"early_data,omitempty"`

	Security    string   `json:"security,omitempty"` // "", tls, reality
	SNI         string   `json:"sni,omitempty"`
	ALPN        []string `json:"alpn,omitempty"`
	Fingerprint string   `json:"fp,omitempty"`
	// HelloFP is the browser ClientHello the app picked for a server whose
	// link names none; it is not part of the link.
	HelloFP string `json:"-"`
	// TryECH asks the core to look up the server's ECH config over DNS
	// (Cloudflare publishes one for most sites) when the link has none.
	TryECH     bool   `json:"-"`
	Insecure   bool   `json:"insecure,omitempty"`
	PublicKey  string `json:"pbk,omitempty"`
	ShortID    string `json:"sid,omitempty"`
	SpiderX    string `json:"spx,omitempty"`
	ECH        string `json:"ech,omitempty"`
	PinnedCert string `json:"pcs,omitempty"`
	HostKey    string `json:"host_key,omitempty"` // ssh: the server's public host key
	VerifyName string `json:"vcn,omitempty"`
	Fragment   bool   `json:"fragment,omitempty"` // link asked for TLS ClientHello fragmentation
	FinalMask  string `json:"fm,omitempty"`       // raw Xray finalmask JSON
	// SplitDownload, for XHTTP behind Cloudflare, is a second edge IP the
	// download goes through while uploads use the first, so no single
	// connection carries a whole exchange for filtering to analyse.
	SplitDownload string `json:"-"`

	Plugin     string `json:"plugin,omitempty"`
	PluginOpts string `json:"plugin_opts,omitempty"`

	Obfs         string `json:"obfs,omitempty"`
	ObfsPassword string `json:"obfs_password,omitempty"`
	UpMbps       int    `json:"up_mbps,omitempty"`
	DownMbps     int    `json:"down_mbps,omitempty"`

	CongestionControl string `json:"cc,omitempty"`
	UDPRelayMode      string `json:"udp_relay_mode,omitempty"`
	DisableSNI        bool   `json:"disable_sni,omitempty"`

	PrivateKey    string   `json:"private_key,omitempty"`
	PeerPublicKey string   `json:"peer_public_key,omitempty"`
	PreSharedKey  string   `json:"psk,omitempty"`
	LocalAddress  []string `json:"local_address,omitempty"`
	Reserved      []int    `json:"reserved,omitempty"`
	MTU           int      `json:"mtu,omitempty"`

	// OpenVPN (VPN Gate) material.
	OVPN *OpenVPN `json:"ovpn,omitempty"`

	// warp-plus methods: "warp", "gool" (WARP in WARP) or "psiphon"; "tor";
	// and usque's "masque" (WARP over MASQUE, HTTP/3) and "masque2" (the
	// same over TCP and HTTP/2, for networks that throttle UDP).
	WPMode    string `json:"wp_mode,omitempty"`
	WPCountry string `json:"wp_country,omitempty"`

	// Source is the original share link, kept for export.
	Source string `json:"-"`
}

// OpenVPN holds the parts of an .ovpn profile sing-box needs.
type OpenVPN struct {
	Network    string   `json:"network"`
	CA         []string `json:"ca,omitempty"`
	Cert       []string `json:"cert,omitempty"`
	Key        []string `json:"key,omitempty"`
	Cipher     string   `json:"cipher,omitempty"`
	Auth       string   `json:"auth,omitempty"`
	TLSCrypt   []string `json:"tls_crypt,omitempty"`
	TLSAuth    []string `json:"tls_auth,omitempty"`
	KeyDir     string   `json:"key_direction,omitempty"`
	Compress   string   `json:"compress,omitempty"`
	DataCipher []string `json:"data_ciphers,omitempty"`
}

// Key identifies a node by everything except its display name.
func (n *Node) Key() string {
	h := sha1.New()
	w := func(s string) { h.Write([]byte(s)); h.Write([]byte{0}) }
	w(n.Proto)
	w(strings.ToLower(n.Server))
	w(strconv.Itoa(n.Port))
	w(n.PortHop)
	w(n.UUID)
	w(n.Password)
	w(n.Username)
	w(n.Method)
	w(n.Flow)
	w(n.Encryption)
	w(n.Transport)
	w(n.HeaderType)
	w(n.Host)
	w(n.Path)
	w(n.ServiceName)
	w(n.Mode)
	w(n.Extra)
	w(n.Security)
	w(n.SNI)
	w(n.PublicKey)
	w(n.ShortID)
	w(n.Plugin)
	w(n.PluginOpts)
	w(n.Obfs)
	w(n.ObfsPassword)
	w(n.PrivateKey)
	w(n.PeerPublicKey)
	w(n.ECH)
	w(n.WPMode)
	w(n.WPCountry)
	if n.OVPN != nil {
		w(n.OVPN.Network)
		w(strings.Join(n.OVPN.CA, ""))
		w(strings.Join(n.OVPN.Cert, ""))
	}
	return hex.EncodeToString(h.Sum(nil)[:10])
}

// Key64 is a compact form of Key for large in-memory indexes.
func Key64(key string) uint64 {
	b, err := hex.DecodeString(key)
	if err != nil || len(b) < 8 {
		s := sha1.Sum([]byte(key))
		return binary.LittleEndian.Uint64(s[:8])
	}
	return binary.LittleEndian.Uint64(b[:8])
}

// UDPBased reports whether the node's own transport is UDP, so a plain TCP
// reachability probe says nothing about it.
func (n *Node) UDPBased() bool {
	switch n.Proto {
	case "hysteria", "hysteria2", "tuic", "wireguard", "warpplus":
		return true
	case "openvpn":
		return n.OVPN != nil && n.OVPN.Network == "udp"
	}
	return n.Transport == "kcp" || n.Transport == "quic"
}

// Endpoint is host:port used for reachability probes.
func (n *Node) Endpoint() string {
	return net.JoinHostPort(n.Server, strconv.Itoa(n.Port))
}

// NetID names the network a server sits in, for learning which ones a
// filter blocks: the /24 of an IPv4 address (below 1<<24), otherwise a hash
// of the name or of the IPv6 /48 (with the top bit set).
func (n *Node) NetID() uint32 {
	if ip := net.ParseIP(n.Server); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return binary.BigEndian.Uint32(v4) >> 8
		}
		return fnv32(ip[:6]) | 1<<31
	}
	return fnv32([]byte(strings.ToLower(n.Server))) | 1<<31
}

func fnv32(b []byte) uint32 {
	h := uint32(2166136261)
	for _, c := range b {
		h = (h ^ uint32(c)) * 16777619
	}
	return h
}

// Engine picks the core able to run this node, preferring sing-box.
func (n *Node) Engine() Engine {
	if n.Validate() != nil {
		return EngineNone
	}
	if n.Proto == "warpplus" {
		return EngineWarpPlus
	}
	if n.needsXray() {
		if n.xraySupported() {
			return EngineXray
		}
		return EngineNone
	}
	return EngineSingBox
}

func (n *Node) needsXray() bool {
	switch n.Transport {
	case "xhttp", "kcp":
		return true
	}
	if n.Proto == "vless" && n.Encryption != "" && n.Encryption != "none" {
		return true
	}
	if n.Transport == "grpc" && n.Mode == "multi" {
		return true
	}
	if n.Transport == "tcp" && n.HeaderType == "http" && n.Security != "" {
		// sing-box's plain HTTP/1.1 transport matches v2ray's HTTP header
		// obfuscation, but with TLS it switches to HTTP/2.
		return true
	}
	if n.Security == "tls" && (n.ECH != "" || n.VerifyName != "" || n.PinnedCert != "") && !n.Insecure {
		return true
	}
	if n.Flow == "xtls-rprx-vision-udp443" {
		return true
	}
	return false
}

func (n *Node) xraySupported() bool {
	switch n.Proto {
	case "vless", "vmess", "trojan":
		return true
	case "shadowsocks":
		return xraySSMethods[n.Method] && n.Plugin == ""
	}
	return false
}

// Validate rejects nodes that would make a core refuse its whole config.
func (n *Node) Validate() error {
	if n.Proto == "warpplus" {
		switch n.WPMode {
		case "warp", "gool", "tor", "masque", "masque2":
			return nil
		case "psiphon":
			if len(n.WPCountry) == 2 {
				return nil
			}
		}
		return fmt.Errorf("bad warp-plus method")
	}
	if n.Proto == "direct" {
		return nil // no server: the device's own connection
	}
	if n.Server == "" || strings.ContainsAny(n.Server, " \t/\\?#@,\"'") {
		return fmt.Errorf("bad server %q", n.Server)
	}
	if n.Port <= 0 || n.Port > 65535 {
		return fmt.Errorf("bad port %d", n.Port)
	}
	if ip := net.ParseIP(n.Server); ip != nil {
		if ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
			return fmt.Errorf("unusable address %s", n.Server)
		}
	} else if !validHostname(n.Server) {
		return fmt.Errorf("bad hostname %q", n.Server)
	}
	switch n.Proto {
	case "vmess", "vless":
		if n.UUID == "" {
			return fmt.Errorf("missing uuid")
		}
	case "trojan", "anytls", "hysteria2":
		if n.Password == "" {
			return fmt.Errorf("missing password")
		}
	case "shadowsocks":
		if n.Password == "" && n.Method != "none" {
			return fmt.Errorf("missing password")
		}
		if !ssMethods[n.Method] {
			return fmt.Errorf("unsupported cipher %q", n.Method)
		}
		if strings.HasPrefix(n.Method, "2022-") && !validSS2022Key(n.Method, n.Password) {
			return fmt.Errorf("bad 2022 key")
		}
		if n.Plugin != "" && n.Plugin != "obfs-local" && n.Plugin != "v2ray-plugin" && n.Plugin != "shadow-tls" {
			return fmt.Errorf("unsupported plugin %q", n.Plugin)
		}
		if n.Plugin == "shadow-tls" && n.pluginOpt("password") == "" {
			return fmt.Errorf("shadow-tls needs a password")
		}
	case "hysteria":
		if n.ObfsPassword == "" && n.Password == "" {
			// auth-less hysteria v1 servers exist; nothing to check
		}
	case "tuic":
		if n.UUID == "" || n.Password == "" {
			return fmt.Errorf("tuic needs uuid and password")
		}
	case "wireguard":
		if !validWGKey(n.PrivateKey) || !validWGKey(n.PeerPublicKey) {
			return fmt.Errorf("bad wireguard key")
		}
		if n.PreSharedKey != "" && !validWGKey(n.PreSharedKey) {
			return fmt.Errorf("bad wireguard psk")
		}
		if len(n.LocalAddress) == 0 {
			return fmt.Errorf("wireguard needs an address")
		}
	case "socks", "http", "direct":
	case "ssh":
		if n.Username == "" || n.Password == "" {
			return fmt.Errorf("ssh needs a user and a password")
		}
	case "openvpn":
		if n.OVPN == nil || len(n.OVPN.CA) == 0 {
			return fmt.Errorf("openvpn needs a CA")
		}
	default:
		return fmt.Errorf("unsupported protocol %q", n.Proto)
	}
	if n.Security == "reality" {
		if !validRealityKey(n.PublicKey) {
			return fmt.Errorf("bad reality public key")
		}
		if !validShortID(n.ShortID) {
			return fmt.Errorf("bad reality short id")
		}
	}
	if n.Flow != "" && n.Flow != "xtls-rprx-vision" && n.Flow != "xtls-rprx-vision-udp443" {
		return fmt.Errorf("unsupported flow %q", n.Flow)
	}
	switch n.Transport {
	case "", "tcp", "ws", "grpc", "http", "httpupgrade", "xhttp", "kcp", "quic":
	default:
		return fmt.Errorf("unsupported transport %q", n.Transport)
	}
	if n.Flow != "" && n.Transport != "" && n.Transport != "tcp" {
		return fmt.Errorf("vision flow needs raw tcp")
	}
	if n.Transport == "quic" && n.Security != "tls" {
		return fmt.Errorf("quic transport needs tls")
	}
	return nil
}

func validHostname(h string) bool {
	if len(h) > 253 || strings.HasPrefix(h, ".") || strings.HasPrefix(h, "-") {
		return false
	}
	h = strings.TrimSuffix(h, ".")
	if !strings.Contains(h, ".") {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, r := range label {
			if !(r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
				return false
			}
		}
	}
	return true
}

var ssMethods = map[string]bool{
	"2022-blake3-aes-128-gcm": true, "2022-blake3-aes-256-gcm": true, "2022-blake3-chacha20-poly1305": true,
	"none": true, "aes-128-gcm": true, "aes-192-gcm": true, "aes-256-gcm": true,
	"chacha20-ietf-poly1305": true, "xchacha20-ietf-poly1305": true,
	"aes-128-ctr": true, "aes-192-ctr": true, "aes-256-ctr": true,
	"aes-128-cfb": true, "aes-192-cfb": true, "aes-256-cfb": true,
	"rc4-md5": true, "chacha20-ietf": true, "xchacha20": true,
}

// Xray understands only the AEAD and 2022 ciphers.
var xraySSMethods = map[string]bool{
	"2022-blake3-aes-128-gcm": true, "2022-blake3-aes-256-gcm": true, "2022-blake3-chacha20-poly1305": true,
	"none": true, "aes-128-gcm": true, "aes-256-gcm": true,
	"chacha20-ietf-poly1305": true, "xchacha20-ietf-poly1305": true,
}

func validSS2022Key(method, password string) bool {
	want := 32
	if method == "2022-blake3-aes-128-gcm" {
		want = 16
	}
	// Multi-user servers use "iPSK:uPSK"; every part must be a valid key.
	for _, part := range strings.Split(password, ":") {
		b, err := decodeB64(part)
		if err != nil || len(b) != want {
			return false
		}
	}
	return true
}

func validWGKey(k string) bool {
	b, err := decodeB64(k)
	return err == nil && len(b) == 32
}

func validRealityKey(k string) bool {
	b, err := decodeB64(k)
	return err == nil && len(b) == 32
}

func validShortID(s string) bool {
	if len(s) > 16 || len(s)%2 != 0 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// Style is a small code for how a server's traffic looks on the wire: its
// security layer and transport. Filtering blocks some styles far more than
// others, so the app learns which ones work on the current network.
func (n *Node) Style() uint8 {
	sec := uint8(0)
	switch n.Security {
	case "tls":
		sec = 1
	case "reality":
		sec = 2
	}
	tr := uint8(0)
	switch n.Transport {
	case "ws":
		tr = 1
	case "grpc":
		tr = 2
	case "http", "h2":
		tr = 3
	case "httpupgrade":
		tr = 4
	case "xhttp", "splithttp":
		tr = 5
	case "kcp", "quic":
		tr = 6
	}
	if n.ICMP() {
		tr = 7
	}
	if n.DNSTunnel() {
		sec, tr = 3, 6 // 31: the one value the kcp wire and this class share
	}
	return sec*8 + tr + 1 + n.portClass()<<5 // 0 means unknown
}

// DNSTunnel reports whether the node travels inside DNS queries (Xray's
// xdns mask) through whatever resolver the network offers.
func (n *Node) DNSTunnel() bool {
	return strings.Contains(n.FinalMask, `"xdns"`)
}

// StyleDNSTunnel reports whether a style byte names a DNS tunnel.
func StyleDNSTunnel(s uint8) bool { return StyleWire(s) == 31 }

// ICMP reports whether the node travels inside ICMP echo packets (Xray's
// xicmp mask): neither TCP nor UDP, so neither probe says anything about it.
func (n *Node) ICMP() bool {
	return strings.Contains(n.FinalMask, `"xicmp"`)
}

// StyleICMP reports whether a style byte names an ICMP carrier.
func StyleICMP(s uint8) bool { return (StyleWire(s)-1)%8 == 7 }

// StyleDNSPort reports whether a style byte names port 53.
func StyleDNSPort(s uint8) bool { return StylePort(s) == 5 }

// portClass groups ports the way filtering treats them: 443 is watched
// hardest, Cloudflare's other HTTPS ports and the plain-HTTP ports less.
func (n *Node) portClass() uint8 {
	switch n.Port {
	case 443:
		return 1
	case 2053, 2083, 2087, 2096, 8443:
		return 2
	case 80, 8080, 8880, 2052, 2082, 2086, 2095:
		return 3
	case 53:
		// The one UDP port left open in the June 2025 shutdown.
		return 5
	case 0:
		return 0
	}
	return 4
}

// StyleWire and StylePort split a Style code into its security and
// transport part and its port class.
func StyleWire(s uint8) uint8 { return s & 31 }
func StylePort(s uint8) uint8 { return s >> 5 }

// fp is the uTLS fingerprint to use: the link's own, else the app's pick.
func (n *Node) fp() string {
	if n.Fingerprint != "" {
		return n.Fingerprint
	}
	return n.HelloFP
}

// pluginOpt reads one key of PluginOpts ("host=a;password=b;version=3").
func (n *Node) pluginOpt(key string) string {
	for _, kv := range strings.Split(n.PluginOpts, ";") {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// NoEndpoint reports whether the node has no server address to probe: the
// sidecar methods (warp-plus, usque, Tor) and the serverless method.
func (n *Node) NoEndpoint() bool { return n.Proto == "warpplus" || n.Proto == "direct" }
