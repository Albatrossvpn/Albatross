package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Albatrossvpn/vp/internal/sources"
)

// longTail serves a collector's list and three long-tail lists, one server
// each, and counts the downloads.
func longTail(t *testing.T, mobile bool) (*Engine, func(string) int) {
	t.Helper()
	var mu sync.Mutex
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		w.Write([]byte("trojan://pw@" + r.URL.Path[1:] + ".example:443?security=tls&type=tcp#x\n"))
	}))
	t.Cleanup(srv.Close)
	list := func(name string, tier int) sources.Source {
		return sources.Source{ID: name, Kind: sources.KindSubscription, URL: srv.URL + "/" + name, Tier: tier, Builtin: true, Enabled: true, Central: true}
	}
	e, err := New(Options{DataDir: t.TempDir(), Mobile: mobile, Sources: []sources.Source{
		list("fresh", sources.TierPriority), list("all-1", sources.TierDeep), list("all-2", sources.TierDeep), list("all-3", sources.TierDeep),
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.pool.Close() })
	e.ctx, e.cancel = context.WithCancel(context.Background())
	t.Cleanup(e.cancel)
	return e, func(path string) int {
		mu.Lock()
		defer mu.Unlock()
		return hits[path]
	}
}

// later moves the last long-tail download back by d.
func later(e *Engine, d time.Duration) {
	e.mu.Lock()
	e.scan.shardAt = e.scan.shardAt.Add(-d)
	e.mu.Unlock()
}

func tail(hits func(string) int) [3]int {
	return [3]int{hits("/all-1"), hits("/all-2"), hits("/all-3")}
}

// A computer reads one long-tail list every six hours, the one read
// longest ago, and each again only after the others had their turn.
func TestLongTailOneListAtATime(t *testing.T) {
	e, hits := longTail(t, false)
	e.refreshDue()
	if hits("/fresh") != 1 || tail(hits) != [3]int{1, 0, 0} {
		t.Fatalf("first refresh: fresh %d, long tail %v", hits("/fresh"), tail(hits))
	}
	e.refreshDue()
	if tail(hits) != [3]int{1, 0, 0} {
		t.Fatalf("a second list right away: %v", tail(hits))
	}
	for _, want := range [][3]int{{1, 1, 0}, {1, 1, 1}, {1, 1, 1}} {
		later(e, 6*time.Hour)
		e.refreshDue()
		if tail(hits) != want {
			t.Fatalf("six hours on: %v, want %v", tail(hits), want)
		}
	}
	e.mu.Lock()
	for i := range e.srcs {
		if e.srcs[i].ID == "all-1" {
			e.srcStats[i].Fetched = e.srcStats[i].Fetched.Add(-48 * time.Hour)
		}
	}
	e.mu.Unlock()
	later(e, 6*time.Hour)
	e.refreshDue()
	if tail(hits) != [3]int{2, 1, 1} {
		t.Fatalf("two days on: %v", tail(hits))
	}
}

// A phone reads the long tail only once nothing is left to test and no
// server works, one list per half hour at most.
func TestLongTailOnPhonesOnlyWhenNothingWorks(t *testing.T) {
	e, hits := longTail(t, true)
	e.refreshDue()
	if hits("/fresh") != 1 || tail(hits) != [3]int{} {
		t.Fatalf("first refresh: fresh %d, long tail %v", hits("/fresh"), tail(hits))
	}
	e.noneLeft()
	e.refreshDue()
	if tail(hits) != [3]int{1, 0, 0} {
		t.Fatalf("nothing works: %v", tail(hits))
	}
	e.refreshDue()
	if tail(hits) != [3]int{1, 0, 0} {
		t.Fatalf("a second list right away: %v", tail(hits))
	}
	// A server works again: no more.
	e.mu.Lock()
	e.good[1] = &Server{Key: "k", Proto: "trojan", LastOK: time.Now()}
	e.mu.Unlock()
	e.noneLeft()
	later(e, time.Hour)
	e.refreshDue()
	if tail(hits) != [3]int{1, 0, 0} {
		t.Fatalf("with a working server: %v", tail(hits))
	}
}

// At start, a long-tail copy older than its turn is neither read nor kept.
func TestWarmStartDropsOldLongTail(t *testing.T) {
	e, _ := longTail(t, true)
	for _, s := range e.srcs {
		if res := e.fetcher.Fetch(e.ctx, s); res.Err != nil {
			t.Fatal(res.Err)
		}
	}
	var old sources.Source
	for _, s := range e.srcs {
		if s.ID == "all-2" {
			old = s
		}
	}
	meta := filepath.Join(e.fetcher.CacheDir, string(old.Kind)+"-"+old.ID+".json")
	stamp := time.Now().Add(-25 * time.Hour).UTC().Format(time.RFC3339)
	if err := os.WriteFile(meta, []byte(`{"url":"`+old.URL+`","fetched_at":"`+stamp+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	e.warmStart(nil)
	if !inPool(e, "all-1.example") || inPool(e, "all-2.example") || !inPool(e, "fresh.example") {
		t.Fatalf("all-1 %v, all-2 %v, fresh %v", inPool(e, "all-1.example"), inPool(e, "all-2.example"), inPool(e, "fresh.example"))
	}
	if _, ok := e.fetcher.Cached(old); ok {
		t.Fatal("the old copy was kept")
	}
}
