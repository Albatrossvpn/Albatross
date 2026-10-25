package engine

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Albatrossvpn/vp/internal/node"
	"github.com/Albatrossvpn/vp/internal/pool"
	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// The network's regime: how hard this network filters right now, found
// with a handful of direct probes and used to order the search. Iran moves
// between ordinary filtering, harder periods, an allowlist where only
// approved names and addresses get out, and national-only internet.
type regime string

const (
	regimeUnknown   regime = ""
	regimeFree      regime = "free"      // blocked sites open without a VPN (abroad, Starlink, a tier with little filtering)
	regimeFiltered  regime = "filtered"  // ordinary blocklist filtering
	regimeHard      regime = "hard"      // the blocklist plus cut flows to foreign servers
	regimeAllowlist regime = "allowlist" // only approved names and addresses get out
	regimeNational  regime = "national"  // only domestic sites answer
	regimeOffline   regime = "offline"   // nothing answers
)

// severity orders regimes from open to closed.
func (r regime) severity() int {
	switch r {
	case regimeFree:
		return 0
	case regimeFiltered:
		return 1
	case regimeHard:
		return 2
	case regimeAllowlist:
		return 3
	case regimeNational:
		return 4
	case regimeOffline:
		return 5
	}
	return -1
}

// regimeProbe holds what each probe found.
type regimeProbe struct {
	Domestic  bool `json:"domestic"`   // a domestic site accepts connections
	Foreign   bool `json:"foreign"`    // data flows from an ordinary foreign site
	Google    bool `json:"google"`     // Google answers (it stayed open in allowlist periods)
	RandomSNI bool `json:"random_sni"` // a TLS hello for a made-up name reaches the server
	Blocked   bool `json:"blocked"`    // a blocked site opens directly
	Poisoned  bool `json:"poisoned"`   // the local resolver fakes answers for blocked names
	UDP       bool `json:"udp"`        // UDP gets out on a port other than 53
	// The side channels a shutdown may leave open, measured rather than
	// assumed: a DNS datagram to a foreign resolver is answered (UDP 53);
	// the network's own resolver still resolves foreign names (so the DNS
	// tunnel has a path); ping reaches a foreign address ("yes", "no", ""
	// when it could not be tried); a known relay inside the country
	// accepts connections.
	UDP53     bool   `json:"udp53"`
	Recursion bool   `json:"recursion"`
	ICMP      string `json:"icmp,omitempty"`
	Relay     bool   `json:"relay"`
	// Country is where this network's direct traffic leaves from, as
	// Cloudflare sees it.
	Country string `json:"country,omitempty"`
}

func (p regimeProbe) classify() regime {
	switch {
	case p.Blocked:
		return regimeFree
	case p.Foreign && p.RandomSNI:
		return regimeFiltered
	case p.RandomSNI:
		return regimeHard
	case p.Google || p.Foreign:
		return regimeAllowlist
	case p.Domestic:
		return regimeNational
	}
	return regimeOffline
}

// Probe targets. Tests point them elsewhere.
var (
	regimeDomestic = []string{"www.aparat.com:443", "www.digikala.com:443", "www.shaparak.ir:443"}
	regimeForeign  = "https://speed.cloudflare.com/__down?bytes=16384"
	regimeGoogle   = "https://www.google.com/generate_204"
	regimeBlocked  = "https://www.youtube.com/generate_204"
	// A second blocked site on another network (Meta's) must open too: one
	// lucky request through a leaky filter used to switch the app to its
	// lightest settings on a filtered network.
	regimeBlocked2 = "https://www.instagram.com/"
	regimeEdge     = "104.16.132.229:443" // a Cloudflare edge
	regimeSTUN     = "stun.l.google.com:19302"
	regimeUDP53    = []string{"8.8.8.8:53", "1.1.1.1:53"}
	regimeICMP     = []string{"1.1.1.1", "8.8.8.8"}
	regimeName     = "www.youtube.com"
	regimeTrace    = "https://www.cloudflare.com/cdn-cgi/trace"
)

// regimeEvery is how often the regime is probed; a change of network
// probes at once, and a network that cuts foreign addresses is probed
// every regimeShut so the app notices the moment they come back.
const regimeEvery, regimeShut = 3 * time.Hour, 10 * time.Minute

// regimeMinGap is the least time between probes asked for on demand.
var regimeMinGap = 2 * time.Minute

// regimeRecheck is how long to wait before confirming a changed regime.
var regimeRecheck = 15 * time.Second

// probeRegime runs every probe directly, outside the VPN, in parallel.
func (e *Engine) probeRegime(ctx context.Context) (regime, regimeProbe) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var p regimeProbe
	var mu sync.Mutex
	var wg sync.WaitGroup
	set := func(f *bool, fn func() bool) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok := fn()
			mu.Lock()
			*f = ok
			mu.Unlock()
		}()
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil, TLSHandshakeTimeout: 6 * time.Second}, Timeout: 8 * time.Second}
	defer client.CloseIdleConnections()
	get := func(u string, want int) bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return false
		}
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			return false
		}
		if want == http.StatusOK {
			// The foreign probe must carry more than the few packets
			// filtering lets through before it cuts a flow.
			n, _ := io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
			return n >= 16<<10
		}
		return true
	}
	// opens reports whether a site answers at all over verified TLS.
	opens := func(u string) bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
		if err != nil {
			return false
		}
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode < 500
	}
	set(&p.Domestic, func() bool {
		var d net.Dialer
		for _, a := range regimeDomestic {
			if c, err := d.DialContext(ctx, "tcp", a); err == nil {
				c.Close()
				return true
			}
		}
		return false
	})
	set(&p.Foreign, func() bool { return get(regimeForeign, http.StatusOK) })
	set(&p.Google, func() bool { return get(regimeGoogle, http.StatusNoContent) })
	set(&p.Blocked, func() bool {
		return get(regimeBlocked, http.StatusNoContent) && opens(regimeBlocked2)
	})
	set(&p.RandomSNI, func() bool { return randomSNIReaches(ctx, regimeEdge) })
	set(&p.Poisoned, func() bool { return e.poisoned(ctx) })
	set(&p.UDP, func() bool { return stunAnswers(ctx, regimeSTUN) })
	set(&p.UDP53, func() bool { return udp53Answers(ctx, regimeUDP53) })
	set(&p.Recursion, func() bool { return e.recursionWorks(ctx) })
	set(&p.Relay, func() bool { return e.relayAnswers(ctx) })
	wg.Add(1)
	go func() {
		defer wg.Done()
		r := icmpReaches(regimeICMP)
		mu.Lock()
		p.ICMP = r
		mu.Unlock()
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, regimeTrace, nil)
		if err != nil {
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		for _, l := range strings.Split(string(b), "\n") {
			if c, ok := strings.CutPrefix(l, "loc="); ok && len(c) == 2 {
				mu.Lock()
				p.Country = c
				mu.Unlock()
			}
		}
	}()
	wg.Wait()
	return p.classify(), p
}

// randomSNIReaches sends a hello for a name that does not exist to a CDN
// edge. The edge refuses it with a TLS alert; filtering that only lets
// approved names through resets or drops it instead.
func randomSNIReaches(ctx context.Context, edge string) bool {
	var b [6]byte
	rand.Read(b[:])
	name := "x" + hex.EncodeToString(b[:]) + ".com"
	var d net.Dialer
	raw, err := d.DialContext(ctx, "tcp", edge)
	if err != nil {
		return false
	}
	defer raw.Close()
	if dl, ok := ctx.Deadline(); ok {
		raw.SetDeadline(dl)
	}
	err = tls.Client(raw, &tls.Config{ServerName: name}).HandshakeContext(ctx)
	var alert tls.AlertError
	// A finished handshake (some edges serve a default certificate) also
	// means the hello got through.
	return err == nil || errors.As(err, &alert) || isCertError(err)
}

func isCertError(err error) bool {
	var v *tls.CertificateVerificationError
	return errors.As(err, &v)
}

// poisoned reports whether the local resolver answers a blocked name with
// one of the censor's addresses (10.10.34.0/23).
func (e *Engine) poisoned(ctx context.Context) bool {
	r := net.DefaultResolver
	if dns := e.dns(); dns != "" {
		r = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "udp", net.JoinHostPort(dns, "53"))
		}}
	}
	addrs, err := r.LookupHost(ctx, regimeName)
	if err != nil {
		return false
	}
	_, censor, _ := net.ParseCIDR("10.10.34.0/23")
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && censor.Contains(ip) {
			return true
		}
	}
	return false
}

// stunAnswers sends a STUN binding request and waits for any answer.
func stunAnswers(ctx context.Context, server string) bool {
	var d net.Dialer
	c, err := d.DialContext(ctx, "udp", server)
	if err != nil {
		return false
	}
	defer c.Close()
	if dl, ok := ctx.Deadline(); ok {
		c.SetDeadline(dl)
	}
	req := make([]byte, 20)
	binary.BigEndian.PutUint16(req[0:], 0x0001) // binding request
	binary.BigEndian.PutUint32(req[4:], 0x2112A442)
	rand.Read(req[8:])
	buf := make([]byte, 512)
	for range 3 {
		if _, err := c.Write(req); err != nil {
			return false
		}
		c.SetReadDeadline(time.Now().Add(time.Second))
		if n, err := c.Read(buf); err == nil && n >= 20 {
			return true
		}
		if ctx.Err() != nil {
			return false
		}
	}
	return false
}

// regimeState is kept on disk so a warning can compare with the past.
type regimeState struct {
	Regime regime      `json:"regime"`
	Probe  regimeProbe `json:"probe"`
	At     time.Time   `json:"at"`
	// Since is when the regime last changed.
	Since time.Time `json:"since"`
	// History is the recent regimes, newest last.
	History []regimeSeen `json:"history"`
}

type regimeSeen struct {
	Regime regime    `json:"regime"`
	At     time.Time `json:"at"`
}

func (e *Engine) loadRegime() {
	b, err := os.ReadFile(filepath.Join(e.dir, "regime.json"))
	if err != nil {
		return
	}
	var s regimeState
	if json.Unmarshal(b, &s) == nil {
		e.mu.Lock()
		e.reg = s
		e.mu.Unlock()
	}
}

func (e *Engine) saveRegime(s regimeState) {
	b, err := json.Marshal(s)
	if err != nil {
		return
	}
	path := filepath.Join(e.dir, "regime.json")
	if os.WriteFile(path+".tmp", b, 0o644) == nil {
		os.Rename(path+".tmp", path)
	}
}

// regimeDue reports whether the regime should be probed again.
func (e *Engine) regimeDue() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	every := regimeEvery
	if e.reg.Regime.severity() >= regimeAllowlist.severity() {
		every = regimeShut
	}
	return e.regNet != e.localDNS || time.Since(e.reg.At) >= every
}

// reprobeRegime probes the regime now, when the network's behaviour
// suggests it changed (every server dead while domestic sites answer), at
// most once every regimeMinGap and never two at once.
func (e *Engine) reprobeRegime(reason string) {
	e.mu.Lock()
	soon := time.Since(e.regAsked) < regimeMinGap
	if !soon {
		e.regAsked = time.Now()
	}
	e.mu.Unlock()
	if soon || e.regProbing.Load() {
		return
	}
	e.logf("info", "Network check (%s)…", reason)
	go e.updateRegime()
}

// updateRegime probes the regime and acts on a change. Everything it
// learns stays on this device.
func (e *Engine) updateRegime() {
	if !e.regProbing.CompareAndSwap(false, true) {
		return
	}
	defer e.regProbing.Store(false)
	e.mu.Lock()
	netKey := e.localDNS
	e.mu.Unlock()
	r, p := e.probeRegime(e.ctx)
	if e.ctx.Err() != nil {
		return
	}
	e.mu.Lock()
	prev := e.reg
	sameNet := e.regNet == netKey
	e.mu.Unlock()
	// One probe on a weak mobile signal can miss the foreign download or
	// the CDN edge and read as a much harder regime (or one lucky request
	// as an open network). A change counts only when a second probe a
	// little later still sees it.
	if prev.Regime != regimeUnknown && r != prev.Regime {
		select {
		case <-e.ctx.Done():
			return
		case <-time.After(regimeRecheck):
		}
		r2, p2 := e.probeRegime(e.ctx)
		if e.ctx.Err() != nil {
			return
		}
		// The second reading wins; one that is back at the previous
		// regime means the first was a fluke.
		r, p = r2, p2
	}
	e.mu.Lock()
	s := regimeState{Regime: r, Probe: p, At: time.Now(), Since: prev.Since, History: append(prev.History, regimeSeen{r, time.Now()})}
	if r != prev.Regime || s.Since.IsZero() {
		s.Since = time.Now()
	}
	if len(s.History) > 48 {
		s.History = s.History[len(s.History)-48:]
	}
	e.reg, e.regNet = s, netKey
	e.mu.Unlock()
	e.saveRegime(s)
	e.regimeChanged(prev, s, sameNet)
	if r != regimeOffline {
		e.crowdLookup(e.ctx)
	}
	if r != prev.Regime {
		e.logf("info", "Network check: %s.", regimeText[r])
	}
	// Filtering tightens for days before a large shutdown. When it gets
	// harder, fetch everything a shutdown would cut off while it still can.
	// Another network reads differently without anything having changed,
	// so only a reading from the same network counts as tightening; a
	// first reading that is already hard counts too, since nothing was
	// fetched ahead for this install yet.
	tightened := sameNet && prev.Regime.severity() >= 0 && r.severity() > prev.Regime.severity()
	first := prev.Regime == regimeUnknown && r.severity() >= regimeHard.severity()
	if (tightened || first) && r.severity() >= regimeHard.severity() && r != regimeOffline {
		go e.prepareForShutdown()
	} else if !sameNet || r.severity() <= regimeFiltered.severity() {
		e.mu.Lock()
		if strings.HasPrefix(e.notice, "Filtering is getting much harder") {
			e.notice = ""
		}
		e.mu.Unlock()
	}
}

// shutdownNotice is shown while only domestic addresses answer.
const shutdownNotice = "Only domestic sites answer on this network (a national-internet shutdown). Albatross is trying its in-country relays, the DNS tunnel and the ping carrier. If a friend's Albatross is connected, ask them for a share code."

// regimeChanged acts on a regime change: entering a shutdown shows what
// the app is doing and lengthens the account's grace; leaving one gives
// every candidate and server the shutdown failed its chances back, since
// those failures said nothing about them.
func (e *Engine) regimeChanged(prev, cur regimeState, sameNet bool) {
	shut := cur.Regime.severity() >= regimeAllowlist.severity() && cur.Regime != regimeOffline
	wasShut := prev.Regime.severity() >= regimeAllowlist.severity() && prev.Regime != regimeOffline
	e.applyGrace(shut)
	e.mu.Lock()
	switch {
	case shut && cur.Regime == regimeNational:
		e.notice = shutdownNotice
	case !shut && e.notice == shutdownNotice:
		e.notice = ""
	}
	e.mu.Unlock()
	if wasShut && !shut && sameNet && !prev.Since.IsZero() {
		n := e.pool.ResetDeadSince(pool.Minutes(prev.Since))
		e.mu.Lock()
		for _, s := range e.good {
			if s.Fails > 0 && s.TestedAt.After(prev.Since) && !s.Domestic {
				s.Fails = 0
			}
		}
		e.mu.Unlock()
		e.logf("info", "Foreign addresses answer again; %d candidates the shutdown failed get their chances back.", n)
		e.poke()
	}
}

var regimeText = map[regime]string{
	regimeFree:      "blocked sites open without a VPN here, so the lightest settings are used",
	regimeFiltered:  "ordinary filtering",
	regimeHard:      "heavy filtering; flows to foreign servers are being cut",
	regimeAllowlist: "only approved sites get out; CDN-fronted servers, Tor and Psiphon are tried first",
	regimeNational:  "only domestic sites answer; in-country relays, the DNS tunnel and the ping carrier are tried first",
	regimeOffline:   "this network reaches nothing",
}

// prepareForShutdown downloads every server list, the Tor bridges and the
// update file, and tells the user the app is ready.
func (e *Engine) prepareForShutdown() {
	e.mu.Lock()
	e.notice = "Filtering is getting much harder here, which often comes before a shutdown. The app has downloaded everything it needs to keep connecting."
	e.mu.Unlock()
	e.logf("warn", "Filtering got harder; downloading every server list and the Tor bridges while they can still be reached.")
	e.mu.Lock()
	for i, s := range e.srcs {
		if s.Central && i < len(e.srcStats) {
			// Due now: the refresh loop downloads and loads them.
			e.srcStats[i].Fetched = time.Time{}
		}
	}
	e.mu.Unlock()
	e.triggerRefill()
	e.fetcher.Fetch(e.ctx, torSource)
	e.checkUpdate()
}

// regimeBonus adjusts a candidate's score for the current regime: when
// only approved addresses get out, servers fronted by a CDN on its ports
// are the ones that can work, and UDP servers are pointless without UDP.
func (r regime) bonus(p regimeProbe, en *pool.Entry) float64 {
	b := 0.0
	if r == regimeAllowlist || r == regimeNational || r == regimeHard {
		wire, port := node.StyleWire(en.Style), node.StylePort(en.Style)
		tr := (wire - 1) % 8
		cdn := (tr == 1 || tr == 2 || tr == 4 || tr == 5) && (port >= 1 && port <= 3)
		switch {
		case en.Flags&pool.FlagDomestic != 0:
			// A relay inside the country: the one address that answers
			// when every foreign one is cut.
			b += 150
		case node.StyleDNSTunnel(en.Style):
			// Queries go through the network's own resolver, which keeps
			// answering when every foreign address is cut; measured.
			if p.Recursion {
				b += 110
			} else {
				b += 30
			}
		case node.StyleICMP(en.Style):
			// Ping stayed open when TCP and UDP to foreign addresses were
			// cut; measured when it could be.
			switch p.ICMP {
			case "yes":
				b += 100
			case "":
				b += 40
			default:
				b -= 40
			}
		case node.StyleDNSPort(en.Style):
			// UDP 53 was the one UDP port left open in the June 2025
			// shutdown; it counts when it is open now.
			if p.UDP53 {
				b += 60
			}
		case cdn:
			b += 80
		case r != regimeHard:
			b -= 80
		}
		if en.Flags&pool.FlagOwn != 0 {
			b += 40
		}
	}
	udpOK := p.UDP || (node.StyleDNSPort(en.Style) && p.UDP53) || en.Flags&pool.FlagDomestic != 0
	if r.severity() >= regimeFiltered.severity() && !udpOK && en.Flags&pool.FlagUDP != 0 && !node.StyleICMP(en.Style) && !node.StyleDNSTunnel(en.Style) {
		b -= 60
	}
	return b
}

// udp53Answers sends a DNS query for a made-up name to foreign resolvers
// on UDP 53 and reports whether any datagram with the query's id comes
// back: the one UDP port a shutdown has left open.
func udp53Answers(ctx context.Context, servers []string) bool {
	var b [6]byte
	rand.Read(b[:])
	name, err := dnsmessage.NewName("x" + hex.EncodeToString(b[:]) + ".com.")
	if err != nil {
		return false
	}
	var idb [2]byte
	rand.Read(idb[:])
	id := binary.BigEndian.Uint16(idb[:])
	q := dnsmessage.Message{Header: dnsmessage.Header{ID: id, RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}}}
	pkt, err := q.Pack()
	if err != nil {
		return false
	}
	var d net.Dialer
	for _, srv := range servers {
		c, err := d.DialContext(ctx, "udp", srv)
		if err != nil {
			continue
		}
		buf := make([]byte, 512)
		for range 2 {
			c.Write(pkt)
			c.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
			if n, err := c.Read(buf); err == nil && n >= 12 && binary.BigEndian.Uint16(buf[:2]) == id {
				c.Close()
				return true
			}
			if ctx.Err() != nil {
				break
			}
		}
		c.Close()
	}
	return false
}

// recursionWorks reports whether the network's resolver still resolves
// foreign names: a made-up name under google.com must come back as "no
// such name" from Google's own servers. A resolver that answers everything
// (a sinkhole) or nothing has no path abroad, and the DNS tunnel none.
func (e *Engine) recursionWorks(ctx context.Context) bool {
	r := net.DefaultResolver
	if dns := e.dns(); dns != "" {
		r = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "udp", net.JoinHostPort(dns, "53"))
		}}
	}
	var b [6]byte
	rand.Read(b[:])
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	_, err := r.LookupHost(ctx, "x"+hex.EncodeToString(b[:])+".google.com")
	var de *net.DNSError
	return errors.As(err, &de) && de.IsNotFound
}

// relayAnswers reports whether a known relay inside the country accepts
// connections: up to five of the servers whose address is domestic.
func (e *Engine) relayAnswers(ctx context.Context) bool {
	var eps []string
	e.mu.Lock()
	for _, s := range e.good {
		if s.Domestic {
			if n := s.Node(); n != nil && !n.NoEndpoint() && !n.UDPBased() {
				eps = append(eps, n.Endpoint())
			}
		}
		if len(eps) >= 5 {
			break
		}
	}
	e.mu.Unlock()
	if len(eps) == 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	got := make(chan bool, len(eps))
	for _, ep := range eps {
		go func(ep string) { got <- tcpAnswers(ctx, ep) }(ep)
	}
	for range eps {
		if <-got {
			return true
		}
	}
	return false
}

// icmpReaches sends an echo to foreign addresses and reports "yes" when a
// reply comes, "no" when none does, and "" when this process may not send
// echoes (Windows without administrator rights, some Linux builds).
func icmpReaches(targets []string) string {
	c, err := icmp.ListenPacket("udp4", "0.0.0.0")
	raw := false
	if err != nil {
		if c, err = icmp.ListenPacket("ip4:icmp", "0.0.0.0"); err != nil {
			return ""
		}
		raw = true
	}
	defer c.Close()
	id := os.Getpid() & 0xffff
	for i, t := range targets {
		ip := net.ParseIP(t)
		if ip == nil {
			continue
		}
		msg := icmp.Message{Type: ipv4.ICMPTypeEcho, Code: 0, Body: &icmp.Echo{ID: id, Seq: i + 1, Data: []byte("albatross")}}
		pkt, err := msg.Marshal(nil)
		if err != nil {
			continue
		}
		var dst net.Addr = &net.UDPAddr{IP: ip}
		if raw {
			dst = &net.IPAddr{IP: ip}
		}
		c.WriteTo(pkt, dst)
	}
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1500)
	for {
		n, _, err := c.ReadFrom(buf)
		if err != nil {
			return "no"
		}
		if m, err := icmp.ParseMessage(1, buf[:n]); err == nil && m.Type == ipv4.ICMPTypeEchoReply {
			return "yes"
		}
	}
}
