package engine

import (
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Albatrossvpn/vp/internal/pool"
	"github.com/Albatrossvpn/vp/internal/sources"
)

// Plans. The account service says which features the signed-in account's
// plan has (account.Info.Features) and which plan brings each of them
// (Info.Plans; backend/src/lib.js FEATURES). The app keeps a feature the
// plan lacks switched off and says which plan has it. The servers that
// cost the most are kept back by the service itself: a plan without the
// edge gets no edge links, one without the own servers no key to their
// lists; the app also stops using the ones it holds when a plan loses
// them, and keeps them in the pool in case it gets them back.

// The features a plan may have beyond connecting.
const (
	FeatureLocations  = "locations"   // choosing the country
	FeatureAdblock    = "adblock"     // Settings.BlockAds
	FeatureBypassApps = "bypass_apps" // Settings.BypassApps
	FeatureProtocols  = "protocols"   // choosing the protocol
	FeatureTUN        = "tun"         // the whole-device mode
	FeatureGPS        = "gps"         // Settings.OverrideGPS
	FeatureEdge       = "edge"        // the edge's servers
	FeatureOwn        = "own"         // the project's own servers (not the relays)
	FeatureLAN        = "lan"         // Settings.AllowLAN
	// FeaturePriority puts the project's own servers, which public lists
	// do not crowd, first in line, in a shutdown too; FeatureSupport is
	// direct support, which the app links to.
	FeaturePriority = "priority"
	FeatureSupport  = "support"
)

// PlanError says a feature does not come with the account's plan.
type PlanError struct {
	Feature string
	Tier    string // the plan it comes with; "" when the service did not say
}

var tierNames = map[string]string{"bronze": "Bronze", "silver": "Silver", "gold": "Gold", "vip": "VIP"}

func (p *PlanError) Error() string {
	if n := tierNames[p.Tier]; n != "" {
		return "This comes with the " + n + " plan and above."
	}
	return "Your plan does not include this."
}

// Can reports whether the signed-in account's plan has the feature. Every
// feature is there in a build without accounts, while nobody is signed in
// (nothing connects then, and the settings stay as they were), and when
// the service names no features (one from before the plans had them).
func (e *Engine) Can(feature string) bool {
	if e.acct == nil {
		return true
	}
	v := e.acct.View()
	if !v.SignedIn || v.Info.Features == nil {
		return true
	}
	return slices.Contains(v.Info.Features, feature)
}

// Locked returns a PlanError when the plan does not have the feature, nil
// when it has.
// Has reports whether the signed-in account's plan names the feature. Unlike
// Can, a build without accounts, or a service that names no features, has
// none: for what only a plan that pays for it gets.
func (e *Engine) Has(feature string) bool {
	if e.acct == nil {
		return false
	}
	v := e.acct.View()
	return v.SignedIn && slices.Contains(v.Info.Features, feature)
}

func (e *Engine) Locked(feature string) error {
	if e.Can(feature) {
		return nil
	}
	return e.planError(feature)
}

func (e *Engine) planError(feature string) error {
	tier := ""
	if e.acct != nil {
		tier = e.acct.View().Info.Plans[feature]
	}
	return &PlanError{Feature: feature, Tier: tier}
}

// planOff switches off in s what the plan does not have and returns those
// features.
func (e *Engine) planOff(s *Settings) []string {
	var off []string
	flag := func(on *bool, f string) {
		if *on && !e.Can(f) {
			*on = false
			off = append(off, f)
		}
	}
	flag(&s.BlockAds, FeatureAdblock)
	flag(&s.BypassApps, FeatureBypassApps)
	flag(&s.OverrideGPS, FeatureGPS)
	flag(&s.AllowLAN, FeatureLAN)
	if s.Protocol != "" && !e.Can(FeatureProtocols) {
		s.Protocol = ""
		off = append(off, FeatureProtocols)
	}
	if s.Mode == "tun" && !e.Can(FeatureTUN) {
		s.Mode = "proxy"
		off = append(off, FeatureTUN)
	}
	return off
}

// planLocked returns the first feature s turns on, where old has it off,
// that the plan does not have; "" when there is none.
func (e *Engine) planLocked(old, s Settings) string {
	on := func(now, was bool, f string) bool { return now && !was && !e.Can(f) }
	switch {
	case on(s.BlockAds, old.BlockAds, FeatureAdblock):
		return FeatureAdblock
	case on(s.BypassApps, old.BypassApps, FeatureBypassApps):
		return FeatureBypassApps
	case on(s.OverrideGPS, old.OverrideGPS, FeatureGPS):
		return FeatureGPS
	case on(s.AllowLAN, old.AllowLAN, FeatureLAN):
		return FeatureLAN
	case s.Protocol != "" && s.Protocol != old.Protocol && !e.Can(FeatureProtocols):
		return FeatureProtocols
	case s.Mode == "tun" && old.Mode != "tun" && !e.Can(FeatureTUN):
		return FeatureTUN
	}
	return ""
}

// accountChanged follows a new answer from the service, a sign-in or a
// sign-out: the icon's plan mark and what the plan allows.
func (e *Engine) accountChanged() {
	e.updateBadge()
	e.applyPlan()
}

// applyPlan brings the app in line with the plan: what the plan lacks is
// switched off, a chosen country goes back to the fastest without
// "locations", and the edge's and the own servers leave the servers in use
// when the plan does not have them (they come back with a plan that has).
func (e *Engine) applyPlan() {
	s := e.Settings()
	if off := e.planOff(&s); len(off) > 0 {
		e.logf("info", "Turned off what the plan does not include: %s.", strings.Join(off, ", "))
		e.UpdateSettings(s)
	}
	e.mu.Lock()
	target := e.target
	e.mu.Unlock()
	if target != "" && !e.Can(FeatureLocations) {
		e.SetTarget("")
	}
	own, edge := e.Can(FeatureOwn), e.Can(FeatureEdge)
	e.mu.Lock()
	// The first time, the servers kept from the last run may hold what the
	// plan lacks.
	first := !e.planned
	lostOwn, gotOwn := !own && (first || e.planOwn), own && !first && !e.planOwn
	lostEdge, gotEdge := !edge && (first || e.planEdge), edge && !first && !e.planEdge
	e.planned, e.planOwn, e.planEdge = true, own, edge
	e.mu.Unlock()
	if lostEdge {
		methodsMu.Lock()
		edgeLinks = nil
		methodsMu.Unlock()
		os.Remove(edgePath(e.dir))
	}
	if gotEdge {
		methodsMu.Lock()
		edgeFetched = time.Time{} // asked for at the next pass
		methodsMu.Unlock()
	}
	if lostOwn || lostEdge {
		e.dropPlanServers(lostOwn, lostEdge)
	}
	if lostOwn || gotOwn {
		// The own servers' lists are read only on a plan with them.
		e.buildSources()
		if gotOwn {
			e.retestOwn()
			if e.ctx != nil {
				go e.refreshListKeys()
			}
		}
		e.triggerRefill()
	}
}

// ownSource reports whether a source is one of the own servers' lists: an
// own list other than the relays', which every plan has.
func ownSource(s sources.Source) bool { return s.Sealed && !s.Relay }

// dropPlanServers takes the own servers and the edge's out of the servers in
// use. The pool keeps their entries; while the plan lacks them they are
// not tested (testable), and a plan that has them again gets them back
// (retestOwn).
func (e *Engine) dropPlanServers(own, edge bool) {
	e.mu.Lock()
	ownLabels := map[string]bool{}
	for _, s := range e.srcs {
		if ownSource(s) {
			ownLabels[s.Label()] = true
		}
	}
	n := 0
	for k, s := range e.good {
		if (own && s.Own && ownLabels[s.Source]) || (edge && s.Source == "edge") {
			delete(e.good, k)
			n++
		}
	}
	e.mu.Unlock()
	if n > 0 {
		e.logf("info", "%d servers the plan does not include are no longer used.", n)
	}
}

// retestOwn makes the own servers' entries the pool holds new again, so a
// plan that has them again tests and uses them without waiting for their
// lists' next download.
func (e *Engine) retestOwn() {
	e.mu.Lock()
	srcs := append([]sources.Source(nil), e.srcs...)
	inUse := map[uint64]bool{}
	for k := range e.good {
		inUse[k] = true
	}
	e.mu.Unlock()
	var keys []uint64
	e.pool.Each(func(en *pool.Entry) {
		if planOwnEntry(en, srcs) && !inUse[en.Key] {
			keys = append(keys, en.Key)
		}
	})
	for _, k := range keys {
		e.pool.Update(k, func(en *pool.Entry) { en.State, en.Fails, en.LastTest = pool.StateNew, 0, 0 })
	}
}

// planOwnEntry reports whether a pool entry came from the own servers'
// lists: the relays' entries do not count, every plan has them.
func planOwnEntry(en *pool.Entry, srcs []sources.Source) bool {
	return en.Flags&pool.FlagOwn != 0 && (int(en.Source) >= len(srcs) || ownSource(srcs[en.Source]))
}
