package engine

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Albatrossvpn/vp/internal/node"
	"github.com/Albatrossvpn/vp/internal/sources"
)

// A plan with priority ranks the project's own servers ahead of public ones
// that are not much faster; without it they rank by their delay alone, and a
// slow own server loses to a fast public one either way.
func TestOwnServersFirstWithPriority(t *testing.T) {
	svc := &accountService{features: []string{FeatureLocations, FeatureOwn}}
	ts := httptest.NewServer(svc)
	t.Cleanup(ts.Close)
	e, err := New(Options{DataDir: t.TempDir(), Sources: []sources.Source{}, AccountURLs: []string{ts.URL}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.pool.Close() })
	now := time.Now()
	add := func(name string, own bool, delay int) {
		link := fmt.Sprintf("vless://b831381d-6324-4d53-ad4f-8cda48b30811@%s.example:443?security=tls&type=tcp#%s", name, name)
		n, err := node.ParseLink(link)
		if err != nil {
			t.Fatal(err)
		}
		e.good[node.Key64(n.Key())] = &Server{Key: n.Key(), Link: link, Name: name, Country: "DE", Own: own, Delay: delay, LastOK: now, TestedAt: now}
	}
	add("public", false, 100)
	add("own-near", true, 350)
	add("own-slow", true, 900)
	first := func() string {
		got := e.bestServers("DE", 3, nil)
		if len(got) != 3 {
			t.Fatalf("want 3 servers, got %d", len(got))
		}
		return got[0].Name
	}
	order := func() []string {
		var out []string
		for _, s := range e.bestServers("DE", 3, nil) {
			out = append(out, s.Name)
		}
		return out
	}

	if e.Has(FeaturePriority) {
		t.Fatal("signed out, yet the plan has priority")
	}
	if got := first(); got != "public" {
		t.Fatalf("no priority: %s first, want public", got)
	}
	if err := e.Login("ada", "pw"); err != nil {
		t.Fatal(err)
	}
	if e.Has(FeaturePriority) || first() != "public" {
		t.Fatalf("a plan without priority: has=%v order=%v", e.Has(FeaturePriority), order())
	}

	svc.mu.Lock()
	svc.features = []string{FeatureLocations, FeatureOwn, FeaturePriority}
	svc.mu.Unlock()
	if _, err := e.acct.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !e.Has(FeaturePriority) {
		t.Fatal("the plan names priority, yet Has says no")
	}
	if got := order(); got[0] != "own-near" || got[1] != "public" || got[2] != "own-slow" {
		t.Fatalf("with priority: %v, want own-near, public, own-slow", got)
	}

	// A service that names no features has everything for Can, but nothing
	// that only a plan that pays for it gets.
	svc.mu.Lock()
	svc.features = nil
	svc.mu.Unlock()
	if _, err := e.acct.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !e.Can(FeaturePriority) || e.Has(FeaturePriority) {
		t.Fatalf("no features named: Can=%v Has=%v", e.Can(FeaturePriority), e.Has(FeaturePriority))
	}
}
