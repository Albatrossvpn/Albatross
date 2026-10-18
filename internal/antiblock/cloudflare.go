package antiblock

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"math/rand"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/cezomagoanlace29861-jpg/vp/internal/node"
)

// Cloudflare's published IPv4 ranges.
var cloudflareNets = mustCIDRs(
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22", "141.101.64.0/18",
	"108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20", "197.234.240.0/22", "198.41.128.0/17",
	"162.158.0.0/15", "104.16.0.0/13", "104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
)

// Ranges commonly used as "clean IP" pools; the scan samples them more.
var preferredNets = mustCIDRs(
	"104.16.0.0/13", "104.24.0.0/14", "172.64.0.0/13", "162.159.0.0/16", "188.114.96.0/20", "141.101.64.0/18",
)

// IsCloudflare reports whether ip belongs to Cloudflare.
func IsCloudflare(ip string) bool {
	p := net.ParseIP(ip)
	return p != nil && inNets(p, cloudflareNets)
}

// cdnPorts are the ports Cloudflare proxies.
var cdnPorts = map[int]bool{
	80: true, 8080: true, 8880: true, 2052: true, 2082: true, 2086: true, 2095: true,
	443: true, 2053: true, 2083: true, 2087: true, 2096: true, 8443: true,
}

// CDNFrontable reports whether a node goes through an HTTP CDN in a way
// that any Cloudflare edge IP can serve: an HTTP-based transport on a port
// Cloudflare proxies, with a real host name to route on.
func CDNFrontable(n *node.Node) bool {
	switch n.Transport {
	case "ws", "httpupgrade", "grpc", "xhttp":
	default:
		return false
	}
	if n.Security == "reality" || !cdnPorts[n.Port] {
		return false
	}
	return Domain(n) != ""
}

// CleanIP is a Cloudflare edge that answered a TLS handshake.
type CleanIP struct {
	IP      string        `json:"ip"`
	Latency time.Duration `json:"latency"`
}

// Scanner finds Cloudflare edge IPs that the local network lets through.
type Scanner struct {
	mu      sync.Mutex
	best    []CleanIP
	scanned time.Time
}

// Best returns the fastest clean IPs from the last scan.
func (s *Scanner) Best() []CleanIP {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]CleanIP(nil), s.best...)
}

// Age is the time since the last scan.
func (s *Scanner) Age() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scanned.IsZero() {
		return time.Duration(1<<62 - 1)
	}
	return time.Since(s.scanned)
}

// Scan tries samples random edge IPs with a real TLS handshake and keeps
// the fastest keep of them.
func (s *Scanner) Scan(ctx context.Context, samples, keep int) []CleanIP {
	ips := sampleIPs(samples)
	var mu sync.Mutex
	var found []CleanIP
	sem := make(chan struct{}, 48)
	var wg sync.WaitGroup
	for _, ip := range ips {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(ip string) {
			defer func() { <-sem; wg.Done() }()
			if d, ok := handshake(ctx, ip); ok {
				mu.Lock()
				found = append(found, CleanIP{IP: ip, Latency: d})
				mu.Unlock()
			}
		}(ip)
	}
	wg.Wait()
	sort.Slice(found, func(i, j int) bool { return found[i].Latency < found[j].Latency })
	if len(found) > keep {
		found = found[:keep]
	}
	s.mu.Lock()
	if len(found) > 0 || s.scanned.IsZero() {
		s.best = found
	}
	s.scanned = time.Now()
	s.mu.Unlock()
	return found
}

func sampleIPs(n int) []string {
	seen := map[string]bool{}
	var out []string
	for tries := 0; len(out) < n && tries < n*4; tries++ {
		nets := cloudflareNets
		if rand.Intn(3) > 0 {
			nets = preferredNets
		}
		ipn := nets[rand.Intn(len(nets))]
		ones, bits := ipn.Mask.Size()
		base := binary.BigEndian.Uint32(ipn.IP.To4())
		off := uint32(rand.Int63n(int64(1) << uint(bits-ones)))
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], base+off)
		if b[3] == 0 || b[3] == 255 {
			continue
		}
		ip := net.IP(b[:]).String()
		if !seen[ip] {
			seen[ip] = true
			out = append(out, ip)
		}
	}
	return out
}

// handshake completes TLS with a Cloudflare-hosted name through ip and
// checks the certificate, which proves the IP is a real, unblocked edge.
func handshake(ctx context.Context, ip string) (time.Duration, bool) {
	ctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	start := time.Now()
	var d net.Dialer
	raw, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(443)))
	if err != nil {
		return 0, false
	}
	defer raw.Close()
	c := tls.Client(raw, &tls.Config{ServerName: "speed.cloudflare.com", MinVersion: tls.VersionTLS12})
	if err := c.HandshakeContext(ctx); err != nil {
		return 0, false
	}
	return time.Since(start), true
}
