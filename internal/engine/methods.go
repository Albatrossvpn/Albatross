package engine

import (
	"sort"
	"sync"
	"time"

	"github.com/cezomagoanlace29861-jpg/vp/internal/node"
	"github.com/cezomagoanlace29861-jpg/vp/internal/tester"
)

// psiphonCountries are the exits warp-plus can ask Psiphon for.
var psiphonCountries = []string{
	"AT", "BE", "BG", "BR", "CA", "CH", "CZ", "DE", "DK", "EE", "ES", "FI", "FR", "GB", "HR", "HU",
	"IE", "IN", "IT", "JP", "LV", "NL", "NO", "PL", "PT", "RO", "RS", "SE", "SG", "SK", "UA", "US",
}

// methodNodes lists the warp-plus methods: plain WARP, WARP in WARP, and
// Psiphon in every country it offers.
func methodNodes() []*node.Node {
	out := []*node.Node{node.WarpPlus("warp", ""), node.WarpPlus("gool", "")}
	for _, c := range psiphonCountries {
		out = append(out, node.WarpPlus("psiphon", c))
	}
	return out
}

var methodsMu sync.Mutex
var methodsTested = map[string]time.Time{}

// refreshWarp tests the warp-plus methods that are due. They are slow to
// start, so only a few run at a time, and the user's chosen country goes
// first.
func (e *Engine) refreshWarp() {
	if e.bin.WarpPlus == "" {
		return
	}
	e.mu.Lock()
	target := e.target
	searching := e.wantConnect && e.conn == nil
	e.mu.Unlock()
	now := time.Now()
	var due []*node.Node
	methodsMu.Lock()
	for _, n := range methodNodes() {
		every := 3 * time.Hour
		if searching || (target != "" && n.WPCountry == target) {
			every = 30 * time.Minute
		}
		if now.Sub(methodsTested[n.Key()]) >= every {
			due = append(due, n)
		}
	}
	methodsMu.Unlock()
	sort.SliceStable(due, func(i, j int) bool {
		return due[i].WPCountry == target && due[j].WPCountry != target
	})
	if len(due) > 4 {
		due = due[:4]
	}
	if len(due) == 0 {
		return
	}
	methodsMu.Lock()
	for _, n := range due {
		methodsTested[n.Key()] = now
	}
	methodsMu.Unlock()
	byNode := map[*node.Node]candidate{}
	for _, n := range due {
		byNode[n] = candidate{key: node.Key64(n.Key()), link: n.Link(), source: -1, node: n}
	}
	t := *e.tester
	t.Batch(e.ctx, "methods", due, func(r tester.Result) {
		c := byNode[r.Node]
		if r.OK {
			if r.Node.WPMode == "psiphon" && r.Country == "" {
				r.Country = r.Node.WPCountry
			}
			e.recordOK(c, r)
			e.mu.Lock()
			if s := e.good[c.key]; s != nil {
				s.Source = "warp-plus"
			}
			e.mu.Unlock()
			e.logf("info", "روش %s کار می‌کند (%d میلی‌ثانیه).", r.Node.Name, r.Delay/time.Millisecond)
		} else {
			e.recordFail(c, r.Err)
		}
	})
}
