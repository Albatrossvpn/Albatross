package engine

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCore answers the parts of the core's API the monitor uses as
// sing-box does: a member's check, a lookup through the core's resolver,
// the choice of the member in use.
type fakeCore struct {
	mu       sync.Mutex
	delayOK  bool
	dnsCode  int // HTTP status of /dns/query
	dnsRcode int // the answer's rcode when dnsCode is 200
	dnsAsks  int
	selected string
}

func (f *fakeCore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case strings.HasPrefix(r.URL.Path, "/proxies/") && strings.HasSuffix(r.URL.Path, "/delay"):
		if !f.delayOK {
			w.WriteHeader(http.StatusGatewayTimeout)
			io.WriteString(w, `{"message":"timeout"}`)
			return
		}
		io.WriteString(w, `{"delay":120}`)
	case r.URL.Path == "/dns/query":
		f.dnsAsks++
		w.WriteHeader(f.dnsCode)
		if f.dnsCode == http.StatusOK {
			fmt.Fprintf(w, `{"Status":%d}`, f.dnsRcode)
		} else {
			io.WriteString(w, `{"message":"exchange failed"}`)
		}
	case r.URL.Path == "/proxies/proxy" && r.Method == http.MethodPut:
		var b struct {
			Name string `json:"name"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		f.selected = b.Name
		w.WriteHeader(http.StatusNoContent)
	case r.URL.Path == "/proxies/sticky":
		w.WriteHeader(http.StatusNoContent)
	case r.URL.Path == "/connections":
		io.WriteString(w, `{"connections":[]}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeCore) asks() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dnsAsks
}

func fakeMember(tag string) *member {
	return &member{tag: tag, country: "DE", alive: true, delay: 120, server: &Server{Name: tag, Proto: "vless",
		Link: "vless://b831381d-6324-4d53-ad4f-8cda48b30811@203.0.113.9:443?security=tls&sni=a.example&type=tcp#" + tag}}
}

// In TUN mode a server whose resolver cannot be reached through it is
// found out: two failed lookups in a row, a failed one being looked at
// again at the next tick, never a cache, and only where lookups go
// through the server at all.
func TestDNSCheckNeedsTwoFailures(t *testing.T) {
	f := &fakeCore{delayOK: true, dnsCode: http.StatusOK, dnsRcode: 3}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := &connection{mode: "tun", clashAddr: srv.Listener.Addr().String(), clashSecret: "s"}
	m := fakeMember("m0")
	e := &Engine{}
	ctx := t.Context()
	if !e.dnsCheck(ctx, c, m) || f.asks() != 1 {
		t.Fatalf("an answer (no such name) is a resolver reached: asks %d", f.asks())
	}
	if !e.dnsCheck(ctx, c, m) || f.asks() != 1 {
		t.Fatalf("asked again before dnsEvery: %d", f.asks())
	}
	f.mu.Lock()
	f.dnsCode = http.StatusInternalServerError
	f.mu.Unlock()
	c.dnsAt = time.Time{}
	if !e.dnsCheck(ctx, c, m) {
		t.Fatal("one failed lookup left the server")
	}
	if e.dnsCheck(ctx, c, m) {
		t.Fatal("two failed lookups in a row did not")
	}
	// A resolver that answers "server failure" fails too.
	f.mu.Lock()
	f.dnsCode, f.dnsRcode = http.StatusOK, 2
	f.mu.Unlock()
	c.dnsAt = time.Time{}
	if !e.dnsCheck(ctx, c, m) || e.dnsCheck(ctx, c, m) {
		t.Fatal("SERVFAIL twice is not a resolver that works")
	}
	before := f.asks()
	// The system proxy hands the names to the server: nothing to check.
	p := &connection{mode: "proxy", clashAddr: c.clashAddr, clashSecret: "s"}
	if !e.dnsCheck(ctx, p, m) || f.asks() != before {
		t.Fatal("checked names in proxy mode")
	}
	// The serverless fallback resolves as it can.
	d := fakeMember("d")
	d.server.Proto, d.server.Link = "direct", ""
	if !e.dnsCheck(ctx, c, d) || f.asks() != before {
		t.Fatal("checked names on the serverless fallback")
	}
	// A core that does not answer says nothing about the server.
	srv.Close()
	c.dnsAt, c.dnsFails = time.Time{}, 1
	if !e.dnsCheck(ctx, c, m) {
		t.Fatal("a core that did not answer counted against the server")
	}
}

func TestStalledOften(t *testing.T) {
	c := &connection{wake: make(chan struct{}, 1)}
	stall := func() {
		c.addTraffic(100, 0)
		c.mu.Lock()
		c.waiting = time.Now().Add(-3 * time.Second)
		c.mu.Unlock()
		c.addTraffic(0, 0)
	}
	for i := range stallsMany {
		if c.stalledOften() {
			t.Fatalf("often after %d stalls", i)
		}
		stall()
	}
	if !c.stalledOften() {
		t.Fatalf("%d stalls within a minute are not often", stallsMany)
	}
	c.mu.Lock()
	c.stallsFrom = time.Now().Add(-2 * stallWindow)
	c.mu.Unlock()
	if c.stalledOften() {
		t.Fatal("old stalls still count")
	}
	stall()
	if c.stalledOften() {
		t.Fatal("one stall after a quiet spell is often")
	}
}
