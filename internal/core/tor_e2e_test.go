package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// TestTorBootstrap reaches the Tor network through the bundle's bridges.
// It needs the network, so it runs only when TOR_BUNDLE names a folder
// build/tor.sh filled.
func TestTorBootstrap(t *testing.T) {
	dir := os.Getenv("TOR_BUNDLE")
	if dir == "" {
		t.Skip("TOR_BUNDLE not set")
	}
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	var lines []string
	r := &Runtime{Bin: Binaries{Tor: filepath.Join(dir, "tor", "tor"+exe)}, Dir: t.TempDir(),
		OnLine: func(_, l string) { lines = append(lines, l) }}
	defer r.StopTor()
	// Bridges on the live Tor network are sometimes slow; like the app, a
	// slow bootstrap carries on into the next attempt.
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	start := time.Now()
	port, err := r.acquireTor(ctx)
	for err != nil && ctx.Err() == nil && time.Since(start) < 5*time.Minute {
		port, err = r.acquireTor(ctx)
	}
	if err != nil {
		for _, l := range lines {
			t.Log(l)
		}
		t.Fatal(err)
	}
	t.Logf("bootstrapped in %v", time.Since(start).Round(time.Second))
	proxy, _ := url.Parse("socks5://127.0.0.1:" + strconv.Itoa(port))
	c := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}, Timeout: time.Minute}
	resp, err := c.Get("https://check.torproject.org/api/ip")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var v struct{ IsTor bool }
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil || !v.IsTor {
		t.Fatalf("not through Tor: %v %+v", err, v)
	}
	r.releaseTor()
}
