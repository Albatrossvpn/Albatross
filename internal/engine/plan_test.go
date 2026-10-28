package engine

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/Albatrossvpn/vp/internal/sources"
)

// What a plan lacks is switched off once the service says so, turning it on
// is refused with the plan that has it, a chosen country goes back to the
// fastest without "locations", and the own servers and the edge's leave the
// servers in use, their lists unread.
func TestPlanFeatures(t *testing.T) {
	svc := &accountService{features: []string{FeatureLocations}}
	ts := httptest.NewServer(svc)
	t.Cleanup(ts.Close)
	own := sources.Source{ID: "own-1", Kind: sources.KindSubscription, URL: "https://lists.example/own-1.bin.sealed", Enabled: true, Builtin: true, Central: true, Sealed: true, Keyed: true}
	relays := sources.Source{ID: "own-0", Kind: sources.KindSubscription, URL: "https://lists.example/own-0.bin.sealed", Enabled: true, Builtin: true, Central: true, Sealed: true, Keyed: true, Relay: true}
	e, err := New(Options{DataDir: t.TempDir(), Sources: []sources.Source{own, relays}, AccountURLs: []string{ts.URL}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.pool.Close() })

	// Signed out, everything may be set.
	s := e.Settings()
	s.BlockAds, s.AllowLAN, s.Protocol = true, true, "vless"
	if err := e.UpdateSettings(s); err != nil {
		t.Fatal(err)
	}
	e.SetTarget("DE")
	e.mu.Lock()
	e.good[1] = &Server{Key: "own", Link: "vless://own", Own: true, Source: own.Label()}
	e.good[2] = &Server{Key: "relay", Link: "vless://relay", Own: true, Domestic: true, Source: relays.Label()}
	e.good[3] = &Server{Key: "edge", Link: "vless://edge", Source: "edge"}
	e.good[4] = &Server{Key: "public", Link: "vless://public", Source: "lists.example/hot.txt.sealed"}
	e.mu.Unlock()

	if err := e.Login("ada", "pw"); err != nil {
		t.Fatal(err)
	}
	got := e.Settings()
	if got.BlockAds || got.AllowLAN || got.Protocol != "" {
		t.Fatalf("what the plan lacks is still on: %+v", got)
	}
	if e.Snapshot().Target != "DE" {
		t.Fatal("the plan has locations, yet the country was dropped")
	}
	e.mu.Lock()
	_, keptRelay := e.good[2]
	_, keptPublic := e.good[4]
	left, ownOn, relaysOn := len(e.good), e.srcs[0].Enabled, e.srcs[1].Enabled
	e.mu.Unlock()
	if left != 2 || !keptRelay || !keptPublic {
		t.Fatalf("servers in use after sign-in: %d (relay %v, public %v)", left, keptRelay, keptPublic)
	}
	if ownOn || !relaysOn {
		t.Fatalf("own servers' list read %v, relays' %v", ownOn, relaysOn)
	}

	// Turning on what the plan lacks is refused with the plan that has it.
	s = e.Settings()
	s.BlockAds = true
	var plan *PlanError
	if err := e.UpdateSettings(s); !errors.As(err, &plan) || plan.Feature != FeatureAdblock || plan.Tier != "silver" {
		t.Fatalf("turning on the ad blocker: %v", err)
	}
	if plan.Error() != "This comes with the Silver plan and above." {
		t.Fatalf("message: %q", plan.Error())
	}
	if e.Settings().BlockAds {
		t.Fatal("refused, yet saved")
	}
	if e.Locked(FeatureLocations) != nil || e.Locked(FeatureTUN) == nil {
		t.Fatal("Locked disagrees with the plan")
	}
	// What the plan has, or turns nothing on, still saves.
	s = e.Settings()
	s.AutoConnect = !s.AutoConnect
	if err := e.UpdateSettings(s); err != nil {
		t.Fatalf("a setting every plan has: %v", err)
	}

	// A plan without locations: the country goes back to the fastest, and
	// connecting to one connects to the fastest.
	svc.mu.Lock()
	svc.features = []string{}
	svc.mu.Unlock()
	if _, err := e.acct.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	e.accountChanged()
	if tg := e.Snapshot().Target; tg != "" {
		t.Fatalf("target %q without locations", tg)
	}

	// A plan with the own servers reads their list again; one from before
	// plans had features (none named) has everything.
	svc.mu.Lock()
	svc.features = nil
	svc.mu.Unlock()
	if _, err := e.acct.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	e.accountChanged()
	e.mu.Lock()
	ownOn = e.srcs[0].Enabled
	e.mu.Unlock()
	if !ownOn || !e.Can(FeatureOwn) || !e.Can(FeatureLAN) {
		t.Fatal("a service naming no features locks something")
	}
}
