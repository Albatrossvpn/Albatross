package node

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// LEAP providers (Riseup) publish their OpenVPN gateways through the
// LEAP API and hand out an anonymous client certificate to anyone who asks,
// so their servers are free and need no account. sources fetches the three
// documents and joins them as one LEAPDoc.
type LEAPDoc struct {
	Provider string          `json:"provider"` // short name, e.g. "riseup"
	EIP      json.RawMessage `json:"eip"`      // /3/config/eip-service.json
	Cert     string          `json:"cert"`     // /3/cert: client key and certificate, PEM
	CA       string          `json:"ca"`       // the provider's CA, PEM
}

// LEAPServer is one gateway and port of a LEAP provider.
type LEAPServer struct {
	Node    *Node
	Country string
}

type leapService struct {
	Locations map[string]struct {
		CountryCode string `json:"country_code"`
	} `json:"locations"`
	Gateways []struct {
		Host     string `json:"host"`
		IP       string `json:"ip_address"`
		Location string `json:"location"`
		Caps     struct {
			Transport []struct {
				Type      string   `json:"type"`
				Protocols []string `json:"protocols"`
				Ports     []string `json:"ports"`
			} `json:"transport"`
		} `json:"capabilities"`
	} `json:"gateways"`
	OpenVPN map[string]any `json:"openvpn_configuration"`
}

// ParseLEAP turns a joined LEAP document into one OpenVPN node per gateway,
// transport protocol and port.
func ParseLEAP(text string) []LEAPServer {
	var doc LEAPDoc
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		return nil
	}
	var svc leapService
	if err := json.Unmarshal(doc.EIP, &svc); err != nil {
		return nil
	}
	ca, cert, key := pemBlocks(doc.CA, "CERTIFICATE"), pemBlocks(doc.Cert, "CERTIFICATE"), pemBlocks(doc.Cert, "PRIVATE KEY")
	if len(ca) == 0 || len(cert) == 0 || len(key) == 0 {
		return nil
	}
	label := strings.ToUpper(doc.Provider[:1]) + doc.Provider[1:]
	var out []LEAPServer
	for _, g := range svc.Gateways {
		if g.IP == "" {
			continue
		}
		country := strings.ToUpper(svc.Locations[g.Location].CountryCode)
		for _, t := range g.Caps.Transport {
			if t.Type != "openvpn" {
				continue
			}
			for _, proto := range t.Protocols {
				if proto != "udp" && proto != "tcp" {
					continue
				}
				for _, ps := range t.Ports {
					port, _ := strconv.Atoi(ps)
					if port <= 0 {
						continue
					}
					v := &OpenVPN{Network: proto, CA: ca, Cert: cert, Key: key}
					if c, ok := svc.OpenVPN["cipher"].(string); ok {
						v.Cipher = strings.ToUpper(c)
					}
					if c, ok := svc.OpenVPN["data-ciphers"].(string); ok {
						for _, dc := range strings.Split(c, ":") {
							v.DataCipher = append(v.DataCipher, strings.ToUpper(dc))
						}
					}
					if a, ok := svc.OpenVPN["auth"].(string); ok {
						v.Auth = strings.ToUpper(a)
					}
					n := &Node{
						Proto:  "openvpn",
						Name:   fmt.Sprintf("%s %s %s/%d", label, g.Location, proto, port),
						Server: g.IP,
						Port:   port,
						OVPN:   v,
						Source: fmt.Sprintf("leap://%s/%s/%s/%d", doc.Provider, g.Host, proto, port),
					}
					if n.Validate() != nil {
						continue
					}
					out = append(out, LEAPServer{Node: n, Country: country})
				}
			}
		}
	}
	// UDP 53 and 1194 first: the ports that stayed open longest.
	sort.SliceStable(out, func(i, j int) bool { return leapRank(out[i].Node) < leapRank(out[j].Node) })
	return out
}

func leapRank(n *Node) int {
	switch {
	case n.OVPN.Network == "udp" && n.Port == 53:
		return 0
	case n.OVPN.Network == "udp":
		return 1
	case n.Port == 80 || n.Port == 443:
		return 2
	}
	return 3
}

// pemBlocks returns the body lines of every PEM block whose type ends with
// kind ("CERTIFICATE", "PRIVATE KEY"), the way ParseOVPN keeps them.
func pemBlocks(pem, kind string) []string {
	var out []string
	var in bool
	for _, raw := range strings.Split(strings.ReplaceAll(pem, "\r", ""), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "-----BEGIN ") && strings.HasSuffix(line, kind+"-----"):
			in = true
			out = append(out, line)
		case in && strings.HasPrefix(line, "-----END "):
			out = append(out, line)
			in = false
		case in && line != "":
			out = append(out, line)
		}
	}
	return out
}
