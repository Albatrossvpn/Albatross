package antiblock

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"io"
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

// Cloudflare IPv6 ranges that serve its proxied sites (the IPv6 twins of
// 104.16.0.0/12 and 172.64.0.0/12). Fewer of them are filtered in Iran,
// where many networks have no IPv6 filtering rules at all.
var cloudflareNets6 = mustCIDRs("2606:4700::6810:0/108", "2606:4700::ac40:0/108")

// Ranges commonly used as "clean IP" pools; the scan samples them more.
var preferredNets = mustCIDRs(
	"104.16.0.0/13", "104.24.0.0/14", "172.64.0.0/13", "162.159.0.0/16", "188.114.96.0/20", "141.101.64.0/18",
)

// IsCloudflare reports whether ip belongs to Cloudflare.
func IsCloudflare(ip string) bool {
	p := net.ParseIP(ip)
	return p != nil && (inNets(p, cloudflareNets) || inNets(p, cloudflareNets6))
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

// hasIPv6 reports whether the device has an IPv6 route to the internet.
// Connecting a UDP socket sends nothing.
var hasIPv6 = func() bool {
	c, err := net.Dial("udp6", "[2606:4700:4700::1111]:53")
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func sampleIPs(n int) []string {
	seen := map[string]bool{}
	var out []string
	if hasIPv6() {
		// A quarter of the samples are IPv6.
		for tries := 0; len(out) < n/4 && tries < n; tries++ {
			ipn := cloudflareNets6[rand.Intn(len(cloudflareNets6))]
			ones, bits := ipn.Mask.Size()
			ip := append(net.IP(nil), ipn.IP.To16()...)
			off := uint32(rand.Int63n(int64(1) << uint(bits-ones)))
			binary.BigEndian.PutUint32(ip[12:], binary.BigEndian.Uint32(ip[12:])+off)
			if s := ip.String(); !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
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

// cleanProbe is fetched through each candidate IP: big enough to need more
// than ten packets, since Iran's filtering lets the first six of a flow to
// a Cloudflare address through before it cuts flows it does not like.
const cleanProbe = 16 << 10

// handshake completes TLS with a Cloudflare-hosted name through ip, checks
// the certificate (which proves the IP is a real edge), and downloads
// cleanProbe bytes over the same connection to prove data keeps flowing.
// The time to finish the handshake is what IPs are ranked by.
func handshake(ctx context.Context, ip string) (time.Duration, bool) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	start := time.Now()
	var d net.Dialer
	raw, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(443)))
	if err != nil {
		return 0, false
	}
	defer raw.Close()
	if dl, ok := ctx.Deadline(); ok {
		raw.SetDeadline(dl)
	}
	const host = "speed.cloudflare.com"
	c := tls.Client(raw, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})
	if err := c.HandshakeContext(ctx); err != nil {
		return 0, false
	}
	took := time.Since(start)
	req := "GET /__down?bytes=" + strconv.Itoa(cleanProbe) + " HTTP/1.1\r\nHost: " + host + "\r\nConnection: close\r\n\r\n"
	if _, err := c.Write([]byte(req)); err != nil {
		return 0, false
	}
	n, _ := io.Copy(io.Discard, io.LimitReader(c, 2*cleanProbe))
	if n < cleanProbe {
		return 0, false
	}
	return took, true
}
