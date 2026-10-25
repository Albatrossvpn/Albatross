package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

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
