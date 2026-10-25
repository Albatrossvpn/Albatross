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

	"github.com/cezomagoanlace29861-jpg/vp/internal/node"
	"github.com/cezomagoanlace29861-jpg/vp/internal/pool"
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
	regimeName     = "www.youtube.com"
	regimeTrace    = "https://www.cloudflare.com/cdn-cgi/trace"
)

// regimeEvery is how often the regime is probed; a change of network
// probes at once.
const regimeEvery = 3 * time.Hour

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
	return e.regNet != e.localDNS || time.Since(e.reg.At) >= regimeEvery
}

// updateRegime probes the regime and acts on a change. Everything it
// learns stays on this device.
func (e *Engine) updateRegime() {
	e.mu.Lock()
	netKey := e.localDNS
	e.mu.Unlock()
	r, p := e.probeRegime(e.ctx)
	if e.ctx.Err() != nil {
		return
	}
	e.mu.Lock()
	prev := e.reg
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
	s := regimeState{Regime: r, Probe: p, At: time.Now(), History: append(prev.History, regimeSeen{r, time.Now()})}
	if len(s.History) > 48 {
		s.History = s.History[len(s.History)-48:]
	}
	e.reg, e.regNet = s, netKey
	e.mu.Unlock()
	e.saveRegime(s)
	if r != regimeOffline {
		e.crowdLookup(e.ctx)
	}
	if r != prev.Regime {
		e.logf("info", "Network check: %s.", regimeText[r])
	}
	// Filtering tightens for days before a large shutdown. When it gets
	// harder, fetch everything a shutdown would cut off while it still can.
	if prev.Regime.severity() >= 0 && prev.Regime.severity() <= regimeFiltered.severity() && r.severity() > prev.Regime.severity() && r != regimeOffline {
		go e.prepareForShutdown()
	}
}

var regimeText = map[regime]string{
	regimeFree:      "blocked sites open without a VPN here, so the lightest settings are used",
	regimeFiltered:  "ordinary filtering",
	regimeHard:      "heavy filtering; flows to foreign servers are being cut",
	regimeAllowlist: "only approved sites get out; CDN-fronted servers, Tor and Psiphon are tried first",
	regimeNational:  "only domestic sites answer; Tor, Psiphon and CDN-fronted servers are tried first",
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
		case node.StyleDNSTunnel(en.Style):
			// Queries go through the network's own resolver, which keeps
			// answering when every foreign address is cut.
			b += 110
		case node.StyleICMP(en.Style):
			// Ping stayed open when TCP and UDP to foreign addresses were cut.
			b += 100
		case node.StyleDNSPort(en.Style):
			// UDP 53 was the one UDP port left open in the June 2025 shutdown.
			b += 60
		case cdn:
			b += 80
		case r != regimeHard:
			b -= 80
		}
	}
	if r.severity() >= regimeFiltered.severity() && !p.UDP && en.Flags&pool.FlagUDP != 0 && !node.StyleICMP(en.Style) && !node.StyleDNSTunnel(en.Style) {
		b -= 60
	}
	return b
}
