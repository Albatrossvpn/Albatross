package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/Albatrossvpn/vp/internal/sources"
)

// The account's slice of the servers is fetched once signed in, kept on
// disk, read into the pool as the source sources.Account (tested first, as
// the hot list's were), and gone from memory, disk and the servers in use
// on sign-out.
func TestAccountSlice(t *testing.T) {
	var mu sync.Mutex
	asked := 0
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/login":
			json.NewEncoder(w).Encode(map[string]any{"token": "t", "account": map[string]any{"username": "ada", "allowed": true}})
		case "/api/v1/servers":
			mu.Lock()
			asked++
			mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"servers": []string{
				"trojan://pw@slice-a.example:443?security=tls&type=tcp#%F0%9F%87%A9%F0%9F%87%AA%20Germany%201",
				"vless://11111111-2222-4333-8444-555555555555@slice-b.example:443?security=tls&type=ws&path=%2F#%F0%9F%87%B3%F0%9F%87%B1%20Netherlands%201",
			}, "reason": ""})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer svc.Close()
	dir := t.TempDir()
	e, err := New(Options{DataDir: dir, Sources: []sources.Source{sources.Account()}, AccountURLs: []string{svc.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer e.pool.Close()
	t.Cleanup(e.forgetSlice)
	e.ctx, e.cancel = context.WithCancel(context.Background())
	defer e.cancel()

	// Signed out: nothing is asked, nothing read.
	e.refreshSlice()
	if text, _ := e.sliceText(e.ctx); text != "" || asked != 0 {
		t.Fatalf("signed out: text %q, asked %d", text, asked)
	}
	// Before Start, as the app signs in: the refresh loop asks.
	ctx := e.ctx
	e.ctx = nil
	if err := e.Login("ada", "pw"); err != nil {
		t.Fatal(err)
	}
	e.ctx = ctx
	e.refreshSlice()
	if _, err := os.Stat(slicePath(dir)); err != nil {
		t.Fatalf("the slice is not kept on disk: %v", err)
	}
	if info, _ := os.Stat(slicePath(dir)); info != nil && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("the slice is readable by others: %v", info.Mode())
	}
	// Not asked again before it is due.
	mu.Lock()
	before := asked
	mu.Unlock()
	e.refreshSlice()
	mu.Lock()
	if asked != before {
		t.Fatalf("asked again at once: %d then %d", before, asked)
	}
	mu.Unlock()

	// Read from disk at the next start, and into the pool.
	e.acctSlice.mu.Lock()
	e.acctSlice.links = nil
	e.acctSlice.mu.Unlock()
	e.loadSlice()
	e.refreshDue()
	if !inPool(e, "slice-a.example") || !inPool(e, "slice-b.example") {
		t.Fatal("the slice's servers are not among the candidates")
	}
	if text, ok := e.cachedText(sources.Account()); !ok || text == "" {
		t.Fatal("the slice is not read at start (warmStart)")
	}
	label := sources.Account().Label()
	if label != "Albatross" {
		t.Fatalf("the source's name says where the servers come from: %q", label)
	}
	e.mu.Lock()
	e.good[1] = &Server{Source: label}
	e.mu.Unlock()

	e.Logout()
	if _, err := os.Stat(slicePath(dir)); !os.IsNotExist(err) {
		t.Fatalf("the slice stayed on disk after signing out: %v", err)
	}
	if text, ok := e.cachedText(sources.Account()); ok || text != "" {
		t.Fatalf("the slice is still read after signing out: %q", text)
	}
	e.mu.Lock()
	_, kept := e.good[1]
	e.mu.Unlock()
	if kept {
		t.Fatal("a server of the slice is still in use after signing out")
	}
	e.loadSlice()
	e.acctSlice.mu.Lock()
	n := len(e.acctSlice.links)
	e.acctSlice.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d servers of the slice came back", n)
	}
}
