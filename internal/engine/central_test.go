package engine

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Albatrossvpn/vp/internal/pool"
	"github.com/Albatrossvpn/vp/internal/sources"
)

// While the collector's list downloads, the other built-in sources are left
// alone; user sources are still read. When it fails, the built-in ones are
// read directly.
func TestCentralReplacesBuiltinSources(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	centralUp := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		up := centralUp
		mu.Unlock()
		if r.URL.Path == "/fresh.txt" && !up {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		w.Write([]byte("trojan://pw@" + r.URL.Path[1:] + ".example:443?security=tls&type=tcp#x\n"))
	}))
	defer srv.Close()
	src := func(name string, central, builtin bool) sources.Source {
		return sources.Source{ID: name, Kind: sources.KindSubscription, URL: srv.URL + "/" + name, Tier: sources.TierPriority, Builtin: builtin, Enabled: true, Central: central}
	}
	e, err := New(Options{DataDir: t.TempDir(), Sources: []sources.Source{
		src("fresh.txt", true, true), src("list", false, true), src("mine", false, false),
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer e.pool.Close()
	e.ctx, e.cancel = context.WithCancel(context.Background())
	defer e.cancel()
	e.refreshDue()
	mu.Lock()
	if hits["/fresh.txt"] != 1 || hits["/list"] != 0 || hits["/mine"] != 1 {
		t.Fatalf("with the collector's list up: %v", hits)
	}
	centralUp = false
	mu.Unlock()

	e.mu.Lock()
	for i := range e.srcStats {
		e.srcStats[i].Fetched = e.srcStats[i].Fetched.Add(-48 * 3600e9)
	}
	e.mu.Unlock()
	e.refreshDue()
	mu.Lock()
	defer mu.Unlock()
	if hits["/list"] != 1 {
		t.Fatalf("built-in source not read while the collector's list is down: %v", hits)
	}
}

// sealedLists serves a copy of the collector's list sealed with key, a
// built-in list, and an account service that hands out key once signed in.
func sealedLists(t *testing.T, key sources.ListKey) (lists, svc *httptest.Server, hits func(string) int) {
	t.Helper()
	sealed, err := sources.SealKeyed(key, "trojan://pw@central.example:443?security=tls&type=tcp#c\n")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	seen := map[string]int{}
	lists = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path]++
		mu.Unlock()
		if r.URL.Path == "/fresh.txt.sealed" {
			w.Write(sealed)
			return
		}
		w.Write([]byte("trojan://pw@list.example:443?security=tls&type=tcp#l\n"))
	}))
	t.Cleanup(lists.Close)
	svc = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/login":
			json.NewEncoder(w).Encode(map[string]any{"token": "t", "account": map[string]any{"username": "ada", "allowed": true}})
		case "/api/v1/listkeys":
			json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{"id": key.ID, "key": hex.EncodeToString(key.Key)}}})
		}
	}))
	t.Cleanup(svc.Close)
	t.Cleanup(forgetListKeys)
	return lists, svc, func(path string) int {
		mu.Lock()
		defer mu.Unlock()
		return seen[path]
	}
}

func sealedSources(base string) []sources.Source {
	return []sources.Source{
		{ID: "fresh", Kind: sources.KindSubscription, URL: base + "/fresh.txt.sealed", Tier: sources.TierPriority, Builtin: true, Enabled: true, Central: true, Keyed: true},
		{ID: "list", Kind: sources.KindSubscription, URL: base + "/list", Tier: sources.TierPriority, Builtin: true, Enabled: true},
	}
}

// inPool says whether a server on host is among the candidates.
func inPool(e *Engine, host string) bool {
	var keys []uint64
	e.pool.Each(func(en *pool.Entry) { keys = append(keys, en.Key) })
	for _, k := range keys {
		if l, ok := e.pool.Link(k); ok && strings.Contains(l, "@"+host+":") {
			return true
		}
	}
	return false
}

// A copy of the collector's list sealed with a key this device lacks
// (signed out, the keys not fetched yet) gives no servers, so the other
// built-in lists are still read; once signing in brings the key, the copy
// already downloaded opens at once and stands in for them again.
func TestSealedCentralListNeedsItsKey(t *testing.T) {
	key := sources.ListKey{ID: 7, Key: bytes.Repeat([]byte{1}, 32)}
	lists, svc, hits := sealedLists(t, key)
	e, err := New(Options{DataDir: t.TempDir(), Sources: sealedSources(lists.URL), AccountURLs: []string{svc.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer e.pool.Close()
	e.ctx, e.cancel = context.WithCancel(context.Background())
	defer e.cancel()

	e.refreshDue()
	if hits("/fresh.txt.sealed") != 1 || hits("/list") != 1 || !inPool(e, "list.example") || inPool(e, "central.example") {
		t.Fatalf("signed out: fresh %d, list %d downloads; central in pool %v, list in pool %v",
			hits("/fresh.txt.sealed"), hits("/list"), inPool(e, "central.example"), inPool(e, "list.example"))
	}

	if err := e.Login("ada", "pw"); err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(10 * time.Second); !inPool(e, "central.example"); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("the copy downloaded before signing in did not open after it")
		}
	}
	if hits("/fresh.txt.sealed") != 1 {
		t.Fatalf("the list was downloaded again rather than opened: %d", hits("/fresh.txt.sealed"))
	}

	e.mu.Lock()
	for i := range e.srcs {
		if e.srcs[i].ID == "list" {
			e.srcStats[i].Fetched = e.srcStats[i].Fetched.Add(-48 * time.Hour)
		}
	}
	e.mu.Unlock()
	e.refreshDue()
	if hits("/list") != 1 {
		t.Fatalf("the built-in list was read again while the collector's list is open: %d", hits("/list"))
	}
}

// At start, a recent copy of the collector's list stands in for the other
// built-in lists' copies only when it opens here.
func TestWarmStartSealedCentralCopy(t *testing.T) {
	key := sources.ListKey{ID: 7, Key: bytes.Repeat([]byte{1}, 32)}
	lists, _, _ := sealedLists(t, key)
	dir := t.TempDir()
	start := func() *Engine {
		e, err := New(Options{DataDir: dir, Sources: sealedSources(lists.URL)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { e.pool.Close() })
		e.ctx, e.cancel = context.WithCancel(context.Background())
		t.Cleanup(e.cancel)
		return e
	}
	e := start()
	for _, s := range e.srcs {
		if res := e.fetcher.Fetch(e.ctx, s); res.Err != nil {
			t.Fatal(res.Err)
		}
	}

	e = start()
	e.loadListKeys()
	e.warmStart(nil)
	if !inPool(e, "list.example") {
		t.Fatal("without the key, the built-in list's copy was not loaded")
	}

	b, _ := json.Marshal([]sources.ListKey{key})
	if err := os.WriteFile(listKeysPath(dir), b, 0o600); err != nil {
		t.Fatal(err)
	}
	e = start()
	e.loadListKeys()
	e.warmStart(nil)
	if !inPool(e, "central.example") || inPool(e, "list.example") {
		t.Fatalf("with the key: central in pool %v, list in pool %v", inPool(e, "central.example"), inPool(e, "list.example"))
	}
}

// The service hands out only the newest keys; the ones the device got
// before stay while a copy it keeps may need them, and an account at a
// limit, which gets none, keeps none.
func TestListKeysKeptAcrossRefreshes(t *testing.T) {
	now := time.Now()
	key := func(age time.Duration, b byte) sources.ListKey {
		return sources.ListKey{ID: uint64(now.Add(-age).Unix()), Key: bytes.Repeat([]byte{b}, 32)}
	}
	newest, recent, old := key(time.Minute, 1), key(10*time.Hour, 2), key(72*time.Hour, 3)
	var mu sync.Mutex
	given := []sources.ListKey{newest}
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/login":
			json.NewEncoder(w).Encode(map[string]any{"token": "t", "account": map[string]any{"username": "ada", "allowed": true}})
		case "/api/v1/listkeys":
			mu.Lock()
			defer mu.Unlock()
			keys := []map[string]any{}
			for _, k := range given {
				keys = append(keys, map[string]any{"id": k.ID, "key": hex.EncodeToString(k.Key)})
			}
			json.NewEncoder(w).Encode(map[string]any{"keys": keys})
		}
	}))
	defer svc.Close()
	t.Cleanup(forgetListKeys)
	e, err := New(Options{DataDir: t.TempDir(), Sources: []sources.Source{}, AccountURLs: []string{svc.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer e.pool.Close()
	if err := e.Login("ada", "pw"); err != nil {
		t.Fatal(err)
	}
	e.ctx, e.cancel = context.WithCancel(context.Background())
	defer e.cancel()
	ids := func() []uint64 {
		listKeysMu.Lock()
		defer listKeysMu.Unlock()
		var out []uint64
		for _, k := range listKeys {
			out = append(out, k.ID)
		}
		return out
	}
	listKeysMu.Lock()
	listKeys, listKeysFetched = []sources.ListKey{recent, old}, time.Time{}
	listKeysMu.Unlock()
	e.refreshListKeys()
	if got := ids(); len(got) != 2 || got[0] != newest.ID || got[1] != recent.ID {
		t.Fatalf("keys after a refresh: %v, want %v %v", got, newest.ID, recent.ID)
	}
	// Kept on disk the same way.
	listKeysMu.Lock()
	listKeys = nil
	listKeysMu.Unlock()
	e.loadListKeys()
	if got := ids(); len(got) != 2 {
		t.Fatalf("keys read back: %v", got)
	}

	mu.Lock()
	given = nil
	mu.Unlock()
	listKeysMu.Lock()
	listKeysFetched = time.Time{}
	listKeysMu.Unlock()
	e.refreshListKeys()
	if got := ids(); len(got) != 0 {
		t.Fatalf("an account at a limit kept keys: %v", got)
	}
}
