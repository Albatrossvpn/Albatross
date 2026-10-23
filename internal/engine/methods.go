package engine

import (
	"context"

	"sort"
	"sync"
	"time"

	"github.com/cezomagoanlace29861-jpg/vp/internal/node"
	"github.com/cezomagoanlace29861-jpg/vp/internal/sources"
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
	if e.bin.WarpPlus == "" && e.bin.Tor == "" {
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
		if e.bin.WarpPlus == "" {
			break
		}
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
	// Tor is the last resort and slow to start, so it is tried while
	// nothing else connects and otherwise only now and then, outside the
	// warp-plus turns.
	if tor := node.WarpPlus("tor", ""); e.bin.Tor != "" {
		every := 12 * time.Hour
		if searching {
			every = 30 * time.Minute
		}
		methodsMu.Lock()
		if now.Sub(methodsTested[tor.Key()]) >= every {
			due = append(due, tor)
		}
		methodsMu.Unlock()
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
	t := e.newTester()
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
				if r.Node.WPMode == "tor" {
					s.Source = "tor"
				}
			}
			e.mu.Unlock()
			e.logf("info", "Method %s works (%d ms).", r.Node.Name, r.Delay/time.Millisecond)
		} else {
			e.recordFail(c, r.Err)
		}
	})
}

// torSource is the collector's copy of Tor's current built-in bridges.
var torSource = sources.Source{ID: "__tor__", Kind: sources.KindSubscription, URL: sources.CentralBase + "tor.json"}

// torBridges returns the last fetched bridge list, fetching it when there
// is none yet.
func (e *Engine) torBridges() []byte {
	if b, ok := e.fetcher.Cached(torSource); ok && time.Since(e.fetcher.CachedAt(torSource)) < 24*time.Hour {
		return []byte(b)
	}
	ctx, cancel := context.WithTimeout(e.ctx, 30*time.Second)
	defer cancel()
	if res := e.fetcher.Fetch(ctx, torSource); res.Err == nil && res.Text != "" {
		return []byte(res.Text)
	}
	b, _ := e.fetcher.Cached(torSource)
	return []byte(b)
}
