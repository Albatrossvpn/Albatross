// Package antiblock holds tricks for networks that actively censor:
// detecting poisoned DNS answers, resolving over DNS-over-HTTPS instead,
// pinning servers to working IPs, and finding Cloudflare edge IPs that
// still pass on the user's ISP.
package antiblock

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Albatrossvpn/vp/internal/node"
)

// Answers Iran's resolvers give for blocked names (the block page).
var poisonNets = mustCIDRs("10.10.34.0/24", "10.10.35.0/24", "10.10.36.0/24")

func mustCIDRs(list ...string) []*net.IPNet {
	var out []*net.IPNet
	for _, s := range list {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			panic(err)
		}
		out = append(out, n)
	}
	return out
}

func inNets(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Poisoned reports whether an answer looks like a censor's fake one.
func Poisoned(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return inNets(ip, poisonNets) || ip.IsLoopback() || ip.IsUnspecified() || ip.IsPrivate()
}

// dohEndpoints answer JSON DNS queries and are addressed by IP, so they need
// no DNS themselves.
var dohEndpoints = []string{
	"https://1.1.1.1/dns-query",
	"https://8.8.8.8/resolve",
	"https://1.0.0.1/dns-query",
	"https://8.8.4.4/resolve",
	"https://9.9.9.9:5053/dns-query",
}

// Resolver resolves server names, falling back to DNS-over-HTTPS when the
// system resolver lies or fails. Answers are cached.
type Resolver struct {
	// Client returns the HTTP client for DoH (through the VPN when
	// connected).
	Client func() *http.Client

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	ip       string
	poisoned bool
	at       time.Time
}

// Result of a lookup.
type Result struct {
	IP       string // empty when nothing usable was found
	Poisoned bool   // the system resolver returned a fake answer
}

// Lookup resolves host to an IPv4 address.
func (r *Resolver) Lookup(ctx context.Context, host string) Result {
	if net.ParseIP(host) != nil {
		return Result{IP: host}
	}
	r.mu.Lock()
	if r.cache == nil {
		r.cache = map[string]cached{}
	}
	if c, ok := r.cache[host]; ok && time.Since(c.at) < 30*time.Minute {
		r.mu.Unlock()
		return Result{IP: c.ip, Poisoned: c.poisoned}
	}
	r.mu.Unlock()
	res := r.lookup(ctx, host)
	r.mu.Lock()
	r.cache[host] = cached{ip: res.IP, poisoned: res.Poisoned, at: time.Now()}
	r.mu.Unlock()
	return res
}

func (r *Resolver) lookup(ctx context.Context, host string) Result {
	lctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	addrs, err := net.DefaultResolver.LookupIPAddr(lctx, host)
	cancel()
	poisoned := false
	if err == nil {
		for _, a := range addrs {
			if ip4 := a.IP.To4(); ip4 != nil {
				if Poisoned(ip4) {
					poisoned = true
					continue
				}
				return Result{IP: ip4.String()}
			}
		}
	}
	// Either no answer or a fake one: ask DoH.
	if ip := r.doh(ctx, host); ip != "" {
		return Result{IP: ip, Poisoned: poisoned}
	}
	return Result{Poisoned: poisoned}
}

func (r *Resolver) doh(ctx context.Context, host string) string {
	if r.Client == nil {
		return ""
	}
	c := r.Client()
	for _, ep := range dohEndpoints {
		ip, err := dohQuery(ctx, c, ep, host)
		if err == nil && ip != "" {
			return ip
		}
	}
	return ""
}

func dohQuery(ctx context.Context, c *http.Client, endpoint, host string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	u := endpoint + "?name=" + url.QueryEscape(host) + "&type=A"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/dns-json")
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", err
	}
	var j struct {
		Answer []struct {
			Type int    `json:"type"`
			Data string `json:"data"`
		} `json:"Answer"`
	}
	if err := json.Unmarshal(b, &j); err != nil {
		return "", err
	}
	for _, a := range j.Answer {
		if a.Type == 1 {
			if ip := net.ParseIP(a.Data).To4(); ip != nil && !Poisoned(ip) {
				return ip.String(), nil
			}
		}
	}
	return "", fmt.Errorf("no A record")
}

// Pin returns a copy of n that connects to ip while presenting the
// original name in TLS SNI and HTTP Host, which is what the server and any
// CDN in front of it route on.
func Pin(n *node.Node, ip string) *node.Node {
	c := *n
	if ip == "" || ip == n.Server {
		return &c
	}
	name := n.Server
	if net.ParseIP(name) == nil {
		if c.Security != "" && c.SNI == "" {
			c.SNI = name
		}
		switch c.Transport {
		case "ws", "httpupgrade", "http", "xhttp", "grpc":
			if c.Host == "" {
				c.Host = name
			}
		case "tcp":
			if c.HeaderType == "http" && c.Host == "" {
				c.Host = name
			}
		}
	}
	c.Server = ip
	return &c
}

// Domain returns the name a node presents to its server or CDN.
func Domain(n *node.Node) string {
	for _, h := range []string{n.SNI, firstHost(n.Host), n.Server} {
		if h != "" && net.ParseIP(h) == nil {
			return h
		}
	}
	return ""
}

func firstHost(h string) string {
	if i := strings.Index(h, ","); i >= 0 {
		h = h[:i]
	}
	return strings.TrimSpace(h)
}
