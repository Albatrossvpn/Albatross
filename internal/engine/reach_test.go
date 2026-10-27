package engine

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// A name whose hints are lies and whose lookup is cut is still reached:
// through an edge address the scan found, and the way is remembered.
func TestDialAnyFindsAWay(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer ts.Close()
	ip, port, _ := net.SplitHostPort(ts.Listener.Addr().String())
	roots := ts.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	defer func(w time.Duration) { wayWait = w }(wayWait)
	wayWait = 2 * time.Second

	dir := t.TempDir()
	store := newReachStore(dir, func() []string { return []string{ip} })
	// The record says a private address: the resolver's lie.
	rec := httpsRecord{addrs: []netip.Addr{netip.MustParseAddr("10.10.34.36")}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, refused, err := dialAny(ctx, "tcp", "example.com", port, rec, store, roots)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if refused {
		t.Fatal("no ECH key, nothing to refuse")
	}
	w, ok := store.last("example.com")
	if !ok || w.Addr != ip || w.Frag {
		t.Fatalf("remembered %+v, %v", w, ok)
	}
	// A new store reads it back and tries it first, before any lie.
	again := newReachStore(dir, nil)
	tries := again.tries("example.com", rec, func() []string { return []string{"10.10.34.36"} })
	if len(tries) == 0 || tries[0].addr != ip {
		t.Fatalf("tries = %+v", tries)
	}
	for _, x := range tries {
		if x.addr == "10.10.34.36" {
			t.Fatal("a private address was kept as a way")
		}
	}

	// Nothing reachable: the deadline holds and the kept way is dropped.
	ts.Close()
	ctx, cancel = context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, _, err := dialAny(ctx, "tcp", "example.com", port, rec, again, roots); err == nil {
		t.Fatal("a closed server was reached")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("gave up after %v", d)
	}
	if _, ok := again.last("example.com"); ok {
		t.Fatal("a way that stopped working was kept")
	}
	if _, err := filepath.Abs(store.path); err != nil {
		t.Fatal(err)
	}
}

// A fragmented hello is sent in several pieces and still completes the
// handshake.
func TestFragmentedHello(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer ts.Close()
	roots := ts.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	defer func(p time.Duration) { fragPause = p }(fragPause)
	fragPause = time.Millisecond

	// Count the writes the first record is sent in.
	raw, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	counter := &countConn{Conn: raw}
	fc := &fragConn{Conn: counter}
	c := tls.Client(fc, &tls.Config{ServerName: "example.com", RootCAs: roots, NextProtos: []string{"http/1.1"}})
	if err := c.Handshake(); err != nil {
		t.Fatal(err)
	}
	c.Close()
	if counter.writes < 3 {
		t.Fatalf("the hello went in %d writes", counter.writes)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := tlsDial(ctx, "tcp", ts.Listener.Addr().String(), "example.com", nil, true, 3*time.Second, roots)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if _, err := tlsDial(ctx, "tcp", ts.Listener.Addr().String(), "wrong.example", nil, true, 3*time.Second, roots); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("a wrong name passed: %v", err)
	}
}

type countConn struct {
	net.Conn
	writes int
}

func (c *countConn) Write(b []byte) (int, error) {
	c.writes++
	return c.Conn.Write(b)
}

// Lies from a resolver are recognised.
func TestBogusAddresses(t *testing.T) {
	for _, s := range []string{"10.10.34.36", "127.0.0.1", "0.0.0.0", "192.168.1.1", "fe80::1"} {
		if !bogus(netip.MustParseAddr(s)) {
			t.Errorf("%s taken for real", s)
		}
	}
	for _, s := range []string{"104.16.132.229", "2606:4700::6810:84e5"} {
		if bogus(netip.MustParseAddr(s)) {
			t.Errorf("%s taken for a lie", s)
		}
	}
	if !errors.Is(errNoWay, errNoWay) {
		t.Fatal()
	}
}

// The first segment of a fragmented hello never holds the server name: the
// first cut falls inside the headers before it, in every try.
func TestHelloCutsHideTheName(t *testing.T) {
	cap := &captureConn{first: make(chan []byte, 1)}
	c := tls.Client(cap, &tls.Config{ServerName: "accounts.example.com", NextProtos: []string{"http/1.1"}})
	go c.Handshake()
	hello := <-cap.first
	at := bytes.Index(hello, []byte("accounts.example.com"))
	if at < 0 {
		t.Fatal("no name in the hello")
	}
	split := 0
	for i := 0; i < 2000; i++ {
		cuts := helloCuts(hello)
		if cuts[len(cuts)-1] != len(hello) || len(cuts) < 3 || len(cuts) > 6 {
			t.Fatalf("cuts %v for %d bytes", cuts, len(hello))
		}
		for k := 1; k < len(cuts); k++ {
			if cuts[k] < cuts[k-1] {
				t.Fatalf("cuts out of order: %v", cuts)
			}
		}
		if cuts[0] >= at {
			t.Fatalf("the first piece (%d bytes) holds the name, which starts at %d", cuts[0], at)
		}
		for _, k := range cuts[1:] {
			if k > at && k < at+len("accounts.example.com") {
				split++
				break
			}
		}
	}
	if split != 2000 {
		t.Fatalf("the name itself was cut in only %d of 2000 hellos", split)
	}
	if from, to, ok := sniSpan(hello); !ok || from != at || to != at+len("accounts.example.com") {
		t.Fatalf("name found at %d-%d (%v), it is at %d", from, to, ok, at)
	}
	// Not a hello, or cut short: no name, and the cuts still cover it all.
	for _, b := range [][]byte{bytes.Repeat([]byte{1}, 300), hello[:60]} {
		if _, _, ok := sniSpan(b); ok {
			t.Fatalf("a name found in %d bytes that hold none", len(b))
		}
		if cuts := helloCuts(b); cuts[len(cuts)-1] != len(b) {
			t.Fatalf("cuts %v", cuts)
		}
	}
}

// captureConn hands over the first write and fails the handshake there.
type captureConn struct {
	net.Conn
	once  sync.Once
	first chan []byte
}

func (c *captureConn) Write(b []byte) (int, error) {
	c.once.Do(func() { c.first <- bytes.Clone(b) })
	return 0, io.ErrClosedPipe
}
func (c *captureConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *captureConn) Close() error                     { return nil }
func (c *captureConn) SetDeadline(time.Time) error      { return nil }
func (c *captureConn) SetReadDeadline(time.Time) error  { return nil }
func (c *captureConn) SetWriteDeadline(time.Time) error { return nil }
