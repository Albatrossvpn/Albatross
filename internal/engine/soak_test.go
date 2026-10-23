package engine

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSoak(t *testing.T) {
	var served, cutAfter atomic.Int64
	content := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c := cutAfter.Load(); c > 0 && served.Add(1) > c {
			hj, _ := w.(http.Hijacker)
			conn, _, _ := hj.Hijack()
			conn.Close()
			return
		}
		w.Write([]byte(strings.Repeat("x", soakChunk)))
	}))
	defer content.Close()
	// A forward proxy standing in for the core's probe port.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp, err := http.DefaultTransport.RoundTrip(&http.Request{Method: r.Method, URL: r.URL, Header: http.Header{}})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}))
	defer proxy.Close()
	old, oldFor, oldEvery := speedURL, soakFor, soakEvery
	speedURL, soakFor, soakEvery = content.URL+"/__down?bytes=", 300*time.Millisecond, 20*time.Millisecond
	defer func() { speedURL, soakFor, soakEvery = old, oldFor, oldEvery }()
	u, _ := url.Parse(proxy.URL)
	c := &connection{probeAddr: u.Host}
	m := &member{tag: "n0"}
	if !c.soak(t.Context(), m) {
		t.Fatal("a steady server failed the endurance check")
	}
	cutAfter.Store(3)
	if c.soak(t.Context(), m) {
		t.Fatal("a server that drops the connection passed")
	}
}
