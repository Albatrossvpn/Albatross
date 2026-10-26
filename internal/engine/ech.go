package engine

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// A direct call to the account service would show its name twice: in the
// DNS query and in the TLS handshake. privateDirect looks the name up over
// DNS-over-HTTPS instead and, when the service publishes an ECH key in its
// HTTPS record (Cloudflare does), sends the name encrypted inside the
// handshake. Where that fails it falls back to an ordinary call, which still
// works, only less privately (see tunnelFirst for why direct comes last).

// dohWire answer DNS-over-HTTPS queries in the DNS wire format (RFC 8484).
// The ones by address need no lookup of their own and carry no name for
// the filtering to read; all are asked at once.
var dohWire = []string{
	"https://dns.google/dns-query", "https://cloudflare-dns.com/dns-query",
	"https://1.1.1.1/dns-query", "https://8.8.8.8/dns-query", "https://9.9.9.9/dns-query", "https://94.140.14.14/dns-query",
}

// dohWait bounds a lookup; all the servers are asked at once.
const dohWait = 3 * time.Second

// How long one try may take to connect and finish its handshake. Where ECH
// is blocked the handshake usually hangs rather than fails, so the ECH try
// gets at most half of what the call has left. A try at one of the other
// ways to reach the service (reach.go) is shorter: there are several.
var echWait, plainWait, wayWait = 4 * time.Second, 8 * time.Second, 4 * time.Second

// httpsRecord is what a name's HTTPS record says about reaching it.
type httpsRecord struct {
	ech   []byte       // ECHConfigList; nil when the name has none
	addrs []netip.Addr // address hints
	at    time.Time
}

// fresh says whether r is recent enough to use: a failed lookup
// (DNS-over-HTTPS is filtered in places) or a refused ECH key is tried again
// after a few minutes, a working key after an hour.
func (r httpsRecord) fresh() bool {
	age := time.Since(r.at)
	return !r.at.IsZero() && (age < 5*time.Minute || r.ech != nil && age < time.Hour)
}

type httpsCache struct {
	doh  *http.Client
	mu   sync.Mutex
	recs map[string]httpsRecord
	busy map[string]chan struct{} // lookups under way
}

// deadlineKey carries a request's deadline to its dial, which net/http
// detaches from the request.
type deadlineKey struct{}

type keepDeadline struct{ http.RoundTripper }

func (k keepDeadline) RoundTrip(req *http.Request) (*http.Response, error) {
	if d, ok := req.Context().Deadline(); ok {
		req = req.WithContext(context.WithValue(req.Context(), deadlineKey{}, d))
	}
	return k.RoundTripper.RoundTrip(req)
}

// newPrivateDirect returns the client for direct account calls, and warm,
// which looks a name up ahead of a call; doh reaches the DNS-over-HTTPS
// servers, store remembers how names were reached (reach.go; nil keeps
// nothing).
func newPrivateDirect(doh *http.Client, store *reachStore) (client *http.Client, warm func(host string)) {
	cache := &httpsCache{doh: doh, recs: map[string]httpsRecord{}, busy: map[string]chan struct{}{}}
	t := &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if d, ok := ctx.Value(deadlineKey{}).(time.Time); ok {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, d)
				defer cancel()
			}
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			c, refused, err := dialAny(ctx, network, host, port, cache.get(ctx, host), store, nil)
			if refused {
				cache.forgetECH(host)
			}
			return c, err
		},
		TLSHandshakeTimeout: 15 * time.Second,
		IdleConnTimeout:     30 * time.Second,
	}
	warm = func(host string) { go cache.get(context.Background(), host) }
	return &http.Client{Transport: keepDeadline{t}}, warm
}

// get returns host's record. One lookup runs at a time for a name; a caller
// whose ctx ends first goes on without the record.
func (h *httpsCache) get(ctx context.Context, host string) httpsRecord {
	for {
		h.mu.Lock()
		r := h.recs[host]
		if r.fresh() {
			h.mu.Unlock()
			return r
		}
		wait, busy := h.busy[host]
		if !busy {
			wait = make(chan struct{})
			h.busy[host] = wait
		}
		h.mu.Unlock()
		if !busy {
			// Not bound to ctx: the answer serves later calls too.
			lctx, cancel := context.WithTimeout(context.Background(), dohWait)
			r, _ = lookupHTTPS(lctx, h.doh, host)
			cancel()
			r.at = time.Now()
			h.mu.Lock()
			h.recs[host] = r
			delete(h.busy, host)
			h.mu.Unlock()
			close(wait)
			return r
		}
		select {
		case <-wait:
		case <-ctx.Done():
			return httpsRecord{}
		}
	}
}

// forgetECH stops using host's ECH key for a few minutes, after the network
// refused or blocked a handshake with it.
func (h *httpsCache) forgetECH(host string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.recs[host]; ok && r.ech != nil {
		r.ech, r.at = nil, time.Now()
		h.recs[host] = r
	}
}

// tlsDial connects to addr and completes a handshake for host within wait:
// with ECH when ech is a key, and with the ClientHello in pieces when frag
// is set (fragment.go). roots is nil (the system's) outside tests.
func tlsDial(ctx context.Context, network, addr, host string, ech []byte, frag bool, wait time.Duration, roots *x509.CertPool) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	var d net.Dialer
	raw, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	var conn net.Conn = raw
	if frag {
		conn = &fragConn{Conn: raw}
	}
	cfg := &tls.Config{ServerName: host, NextProtos: []string{"http/1.1"}, EncryptedClientHelloConfigList: ech, RootCAs: roots}
	c := tls.Client(conn, cfg)
	if err := c.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, err
	}
	return c, nil
}

// echFor bounds an ECH try to half of what ctx has left.
func echFor(ctx context.Context) time.Duration {
	wait := echWait
	if d, ok := ctx.Deadline(); ok && time.Until(d)/2 < wait {
		wait = time.Until(d) / 2
	}
	return wait
}

// dialPrivate connects to host with ECH when rec has a key, at rec's
// addresses when it has some (so the system's resolver is not asked).
// refused says the ECH handshake failed and an ordinary one was made
// instead. roots is nil (the system's) outside tests.
func dialPrivate(ctx context.Context, network, host, port string, rec httpsRecord, roots *x509.CertPool) (c net.Conn, refused bool, err error) {
	targets := []string{net.JoinHostPort(host, port)}
	if len(rec.addrs) > 0 {
		targets = targets[:0]
		for _, a := range rec.addrs {
			targets = append(targets, net.JoinHostPort(a.String(), port))
		}
	}
	handshake := func(ech []byte, wait time.Duration) (net.Conn, error) {
		if ech != nil {
			wait = min(wait, echFor(ctx))
		}
		err := errors.New("no address")
		for _, t := range targets {
			var c net.Conn
			if c, err = tlsDial(ctx, network, t, host, ech, false, wait, roots); err == nil {
				return c, nil
			}
			// Another address is worth a try only for a connection that
			// failed outright, not a handshake that did.
			var ne net.Error
			if !errors.As(err, &ne) || ctx.Err() != nil {
				break
			}
		}
		return nil, err
	}
	if rec.ech == nil {
		c, err = handshake(nil, plainWait)
		return c, false, err
	}
	c, err = handshake(rec.ech, echWait)
	var rej *tls.ECHRejectionError
	if errors.As(err, &rej) && len(rej.RetryConfigList) > 0 {
		// The key changed since the record was read.
		c, err = handshake(rej.RetryConfigList, echWait)
	}
	if err == nil || ctx.Err() != nil {
		return c, false, err
	}
	// ECH is refused or blocked on this network.
	c, err = handshake(nil, plainWait)
	return c, true, err
}

// dialAny connects to host by whatever way gets through (reach.go): ECH at
// the record's first address when the name has a key, then each way in
// turn, plain and with a fragmented hello, until ctx ends. refused says
// an ECH handshake failed. What works is remembered in store.
func dialAny(ctx context.Context, network, host, port string, rec httpsRecord, store *reachStore, roots *x509.CertPool) (net.Conn, bool, error) {
	refused := false
	if rec.ech != nil {
		target := host
		for _, a := range rec.addrs {
			if !bogus(a) {
				target = a.String()
				break
			}
		}
		c, err := tlsDial(ctx, network, net.JoinHostPort(target, port), host, rec.ech, false, echFor(ctx), roots)
		var rej *tls.ECHRejectionError
		if errors.As(err, &rej) && len(rej.RetryConfigList) > 0 {
			c, err = tlsDial(ctx, network, net.JoinHostPort(target, port), host, rej.RetryConfigList, false, echFor(ctx), roots)
		}
		if err == nil {
			store.remember(host, target, false)
			return c, false, nil
		}
		if ctx.Err() != nil {
			return nil, false, err
		}
		refused = true
	}
	last, hasLast := store.last(host)
	err := errNoWay
	for _, t := range store.tries(host, rec, func() []string { return systemAddrs(ctx, host) }) {
		if ctx.Err() != nil {
			break
		}
		wait := wayWait
		if d, ok := ctx.Deadline(); ok && time.Until(d) < wait {
			wait = time.Until(d)
		}
		c, e := tlsDial(ctx, network, net.JoinHostPort(t.addr, port), host, nil, t.frag, wait, roots)
		if e == nil {
			store.remember(host, t.addr, t.frag)
			return c, refused, nil
		}
		err = e
		if hasLast && t.addr == last.Addr && t.frag == last.Frag {
			store.forget(host)
		}
	}
	return nil, refused, err
}

// lookupHTTPS asks the DNS-over-HTTPS servers for host's HTTPS record.
func lookupHTTPS(ctx context.Context, client *http.Client, host string) (httpsRecord, error) {
	name, err := dnsmessage.NewName(host + ".")
	if err != nil {
		return httpsRecord{}, err
	}
	q := dnsmessage.Message{
		Header:    dnsmessage.Header{RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: name, Type: dnsmessage.TypeHTTPS, Class: dnsmessage.ClassINET}},
	}
	packed, err := q.Pack()
	if err != nil {
		return httpsRecord{}, err
	}
	q64 := base64.RawURLEncoding.EncodeToString(packed)
	type answer struct {
		r   httpsRecord
		err error
	}
	got := make(chan answer, len(dohWire))
	for _, s := range dohWire {
		go func() {
			b, err := dohGet(ctx, client, s+"?dns="+q64)
			if err != nil {
				got <- answer{err: err}
				return
			}
			r, err := parseHTTPS(b)
			got <- answer{r, err}
		}()
	}
	err = errors.New("no DNS-over-HTTPS server")
	for range dohWire {
		a := <-got
		if a.err == nil {
			return a.r, nil
		}
		err = a.err
	}
	return httpsRecord{}, err
}

func dohGet(ctx context.Context, client *http.Client, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/dns-message")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<10))
}

// parseHTTPS reads the first HTTPS record of a DNS answer.
func parseHTTPS(b []byte) (httpsRecord, error) {
	var m dnsmessage.Message
	if err := m.Unpack(b); err != nil {
		return httpsRecord{}, err
	}
	for _, a := range m.Answers {
		h, ok := a.Body.(*dnsmessage.HTTPSResource)
		if !ok {
			continue
		}
		var r httpsRecord
		if v, ok := h.GetParam(dnsmessage.SVCParamECH); ok && len(v) > 0 {
			r.ech = bytes.Clone(v)
		}
		// IPv4 first: many networks here have no IPv6.
		for _, p := range []struct {
			key  dnsmessage.SVCParamKey
			size int
		}{{dnsmessage.SVCParamIPv4Hint, 4}, {dnsmessage.SVCParamIPv6Hint, 16}} {
			v, _ := h.GetParam(p.key)
			for size := p.size; len(v) >= size; v = v[size:] {
				if a, ok := netip.AddrFromSlice(v[:size]); ok {
					r.addrs = append(r.addrs, a)
				}
			}
		}
		return r, nil
	}
	return httpsRecord{}, errors.New("no HTTPS record")
}
