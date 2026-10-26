package engine

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// The account service runs behind Cloudflare, which serves every name it
// proxies from every one of its edge addresses: the name only has to reach
// the edge inside the handshake. So when the service's name cannot be
// looked up (the resolver lies, DNS-over-HTTPS is cut) or its usual
// addresses are blocked, the call can go to any edge address the network
// lets through: the ones the clean-address scan found, a few well-known
// ones, or the one that worked last time, which is kept on disk. And when
// the handshake itself is cut for the name it carries, the ClientHello is
// sent in pieces, which most of the filtering here does not reassemble.

// A ways is where a name was reached last: the address and whether the
// handshake had to be fragmented. It is tried first the next time.
type ways struct {
	Addr string `json:"addr"`
	Frag bool   `json:"frag"`
	At   int64  `json:"at"`
}

// reachStore remembers how each name was reached, in dir/reach.json.
type reachStore struct {
	path  string
	clean func() []string // edge addresses the network lets through, best first
	mu    sync.Mutex
	known map[string]ways
}

func newReachStore(dir string, clean func() []string) *reachStore {
	s := &reachStore{path: filepath.Join(dir, "reach.json"), clean: clean, known: map[string]ways{}}
	if b, err := os.ReadFile(s.path); err == nil {
		json.Unmarshal(b, &s.known)
	}
	return s
}

func (s *reachStore) last(host string) (ways, bool) {
	if s == nil {
		return ways{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.known[host]
	return w, ok && time.Since(time.Unix(w.At, 0)) < 30*24*time.Hour
}

// remember keeps what reached host, when it is not what is kept already.
func (s *reachStore) remember(host, addr string, frag bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if w, ok := s.known[host]; ok && w.Addr == addr && w.Frag == frag && time.Since(time.Unix(w.At, 0)) < time.Hour {
		return
	}
	s.known[host] = ways{Addr: addr, Frag: frag, At: time.Now().Unix()}
	if b, err := json.Marshal(s.known); err == nil {
		os.WriteFile(s.path, b, 0o600)
	}
}

// forget drops what was kept for host, after it stopped working.
func (s *reachStore) forget(host string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.known[host]; ok {
		delete(s.known, host)
		if b, err := json.Marshal(s.known); err == nil {
			os.WriteFile(s.path, b, 0o600)
		}
	}
}

// edgeAddrs are Cloudflare edge addresses to try when nothing better is
// known: cloudflare.com's own, and ones widely found open in Iran.
var edgeAddrs = []string{"104.16.132.229", "104.16.133.229", "188.114.96.3", "188.114.97.3", "104.16.123.96", "172.64.153.1"}

// bogus says an address a resolver gave is a lie: the filtering here
// answers with a private address, which leads to a block page or nowhere.
func bogus(a netip.Addr) bool {
	return !a.IsValid() || a.IsPrivate() || a.IsLoopback() || a.IsUnspecified() || a.IsLinkLocalUnicast() || a.IsMulticast()
}

// systemAddrs asks the system's resolver for host, dropping lies.
func systemAddrs(ctx context.Context, host string) []string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		if a = a.Unmap(); !bogus(a) {
			out = append(out, a.String())
		}
	}
	return out
}

// A try is one way to reach a name: an address, and whether to fragment
// the ClientHello. ECH, when the name has a key, is tried on the first
// address before any of these.
type try struct {
	addr string
	frag bool
}

// tries lists the ways to reach host, best first: what worked last time,
// then the record's hints plain and fragmented, then the clean edge
// addresses, the well-known ones, and the system resolver's answer. ask
// gives the system resolver's addresses when they are reached.
func (s *reachStore) tries(host string, rec httpsRecord, ask func() []string) []try {
	var out []try
	seen := map[try]bool{}
	add := func(addr string, frag bool) {
		t := try{addr, frag}
		if addr != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	if w, ok := s.last(host); ok {
		add(w.Addr, w.Frag)
	}
	var hints []string
	for _, a := range rec.addrs {
		if !bogus(a) {
			hints = append(hints, a.String())
		}
	}
	for _, frag := range []bool{false, true} {
		for _, a := range hints {
			add(a, frag)
		}
	}
	var clean []string
	if s != nil && s.clean != nil {
		clean = s.clean()
	}
	if len(clean) > 3 {
		clean = clean[:3]
	}
	known := append([]string(nil), edgeAddrs...)
	rand.Shuffle(len(known), func(i, j int) { known[i], known[j] = known[j], known[i] })
	if len(known) > 2 {
		known = known[:2]
	}
	for _, frag := range []bool{false, true} {
		for _, a := range append(clean, known...) {
			add(a, frag)
		}
	}
	if ask != nil {
		for _, a := range ask() {
			if ip, err := netip.ParseAddr(a); err != nil || bogus(ip.Unmap()) {
				continue
			}
			add(a, false)
			add(a, true)
		}
	}
	return out
}

// errNoWay means no way to reach the name worked before the deadline.
var errNoWay = errors.New("the account service could not be reached in any way")
