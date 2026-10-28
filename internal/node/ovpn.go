package node

import (
	"encoding/base64"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
)

// ParseOVPN reads the parts of an OpenVPN client profile sing-box can use.
func ParseOVPN(profile, name string) (*Node, error) {
	v := &OpenVPN{Network: "udp"}
	n := &Node{Proto: "openvpn", Name: cleanName(name), OVPN: v}
	blocks := map[string][]string{}
	var inBlock string
	for _, raw := range strings.Split(strings.ReplaceAll(profile, "\r", ""), "\n") {
		line := strings.TrimSpace(raw)
		if inBlock != "" {
			if line == "</"+inBlock+">" {
				inBlock = ""
				continue
			}
			if line != "" && !strings.HasPrefix(line, "#") {
				blocks[inBlock] = append(blocks[inBlock], line)
			}
			continue
		}
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if strings.HasPrefix(line, "<") && strings.HasSuffix(line, ">") && !strings.HasPrefix(line, "</") {
			inBlock = strings.Trim(line, "<>")
			continue
		}
		f := strings.Fields(line)
		switch f[0] {
		case "proto":
			if len(f) > 1 && strings.HasPrefix(f[1], "tcp") {
				v.Network = "tcp"
			}
		case "remote":
			if len(f) > 1 && n.Server == "" {
				n.Server = f[1]
				n.Port = 1194
				if len(f) > 2 {
					n.Port, _ = strconv.Atoi(f[2])
				}
				if len(f) > 3 && strings.HasPrefix(f[3], "tcp") {
					v.Network = "tcp"
				}
			}
		case "port":
			if len(f) > 1 && n.Port == 0 {
				n.Port, _ = strconv.Atoi(f[1])
			}
		case "cipher":
			if len(f) > 1 {
				v.Cipher = strings.ToUpper(f[1])
			}
		case "data-ciphers", "ncp-ciphers":
			if len(f) > 1 {
				for _, c := range strings.Split(f[1], ":") {
					v.DataCipher = append(v.DataCipher, strings.ToUpper(c))
				}
			}
		case "auth":
			if len(f) > 1 {
				v.Auth = strings.ToUpper(f[1])
			}
		case "key-direction":
			if len(f) > 1 {
				v.KeyDir = f[1]
			}
		case "comp-lzo", "compress":
			v.Compress = f[0]
		case "auth-user-pass":
			// Credentials are not part of the profile; such servers are
			// skipped because the user has none to give.
			return nil, ErrUnsupported
		}
	}
	v.CA = blocks["ca"]
	v.Cert = blocks["cert"]
	v.Key = blocks["key"]
	v.TLSCrypt = blocks["tls-crypt"]
	v.TLSAuth = blocks["tls-auth"]
	if n.Server == "" || len(v.CA) == 0 {
		return nil, fmt.Errorf("incomplete openvpn profile")
	}
	if err := n.Validate(); err != nil {
		return nil, err
	}
	return n, nil
}

// VPNGateServer is one row of the VPN Gate public list.
type VPNGateServer struct {
	Node    *Node
	Country string
	Score   int
	PingMS  int
	Speed   int
}

// ParseVPNGate reads https://www.vpngate.net/api/iphone/.
func ParseVPNGate(text string) (out []VPNGateServer) {
	defer func() {
		if r := recover(); r != nil {
			out = nil // a parser's bug on the list leaves it out, not the app
		}
	}()
	return parseVPNGate(text)
}

func parseVPNGate(text string) []VPNGateServer {
	var rows []string
	for _, l := range strings.Split(text, "\n") {
		if l == "" || l[0] == '*' || l[0] == '#' {
			continue
		}
		rows = append(rows, l)
	}
	r := csv.NewReader(strings.NewReader(strings.Join(rows, "\n")))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	recs, _ := r.ReadAll()
	var out []VPNGateServer
	for _, rec := range recs {
		if len(rec) < 15 {
			continue
		}
		profile, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rec[14]))
		if err != nil {
			continue
		}
		country := strings.ToUpper(strings.TrimSpace(rec[6]))
		name := "VPN Gate " + country + " " + rec[0]
		n, err := ParseOVPN(string(profile), name)
		if err != nil {
			continue
		}
		n.Source = "vpngate://" + rec[1] + "/" + rec[0]
		score, _ := strconv.Atoi(rec[2])
		ping, _ := strconv.Atoi(rec[3])
		speed, _ := strconv.Atoi(rec[4])
		out = append(out, VPNGateServer{Node: n, Country: country, Score: score, PingMS: ping, Speed: speed})
	}
	return out
}
