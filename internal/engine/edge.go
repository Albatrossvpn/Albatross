package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Albatrossvpn/vp/internal/account"
	"github.com/Albatrossvpn/vp/internal/node"
	"github.com/Albatrossvpn/vp/internal/tester"
)

// The edge is the account service's own server: a Cloudflare Worker that
// carries VLESS over WebSocket (edge/README.md). Signed-in users get its
// links from the service; the app keeps them on disk and tests them like
// the other methods. Being an ordinary Cloudflare site, the edge gets the
// same help as any CDN server: clean edge addresses and ECH.

const (
	edgeRefresh = time.Hour        // how often the service is asked for the links
	edgeEvery   = 3 * time.Hour    // how often a working edge is retested
	edgeSearch  = 30 * time.Minute // ... while nothing connects
	edgeWait    = 20 * time.Second
)

var (
	edgeFetched time.Time // guarded by methodsMu
	edgeLinks   []string  // guarded by methodsMu
)

func edgePath(dir string) string { return filepath.Join(dir, "edge.json") }

// loadEdge reads the links kept from the last answer.
func (e *Engine) loadEdge() {
	b, err := os.ReadFile(edgePath(e.dir))
	if err != nil {
		return
	}
	var links []string
	if json.Unmarshal(b, &links) == nil {
		methodsMu.Lock()
		edgeLinks = links
		methodsMu.Unlock()
	}
}

// refreshEdge asks the service for the edge's links when due and tests the
// ones due. The service is asked through the VPN when it is up; otherwise
// only when no links are kept yet, through servers before going direct
// (tunnelFirst), as signing in does.
func (e *Engine) refreshEdge() {
	if e.acct == nil || !e.acct.View().SignedIn || e.bin.SingBox == "" || !e.Can(FeatureEdge) {
		return
	}
	e.mu.Lock()
	searching := e.wantConnect && e.conn == nil
	e.mu.Unlock()
	now := time.Now()
	methodsMu.Lock()
	links, due := edgeLinks, now.Sub(edgeFetched) >= edgeRefresh
	methodsMu.Unlock()
	if due && (e.proxyClient() != nil || len(links) == 0) {
		ctx, cancel := context.WithTimeout(e.ctx, edgeWait+edgeWait)
		var got []string
		err := e.tunnelFirst(ctx, edgeWait, func(ctx context.Context) error {
			var err error
			got, err = e.acct.Edge(ctx)
			return err
		})
		cancel()
		switch {
		case err == nil:
			methodsMu.Lock()
			edgeFetched, edgeLinks = now, got
			methodsMu.Unlock()
			links = got
			if b, err := json.Marshal(got); err == nil {
				os.WriteFile(edgePath(e.dir), b, 0o600)
			}
			if len(got) > 0 {
				e.logf("info", "The edge has %d addresses.", len(got))
			}
		case errors.Is(err, account.ErrSignedOut), e.ctx.Err() != nil:
			return
		default:
			// Not again on the next pass: the attempt starts cores and may
			// end in a direct call. Ten minutes between tries.
			methodsMu.Lock()
			edgeFetched = now.Add(-edgeRefresh + 10*time.Minute)
			methodsMu.Unlock()
		}
	}
	var nodes []*node.Node
	for _, l := range links {
		n, err := node.ParseLink(l)
		if err != nil || n.Validate() != nil {
			continue
		}
		every := edgeEvery
		if searching {
			every = edgeSearch
		}
		methodsMu.Lock()
		if now.Sub(methodsTested[n.Key()]) >= every {
			nodes = append(nodes, n)
			methodsTested[n.Key()] = now
		}
		methodsMu.Unlock()
	}
	if len(nodes) == 0 {
		return
	}
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].Port == 443 && nodes[j].Port != 443 })
	byNode := map[*node.Node]candidate{}
	for _, n := range nodes {
		byNode[n] = candidate{key: node.Key64(n.Key()), link: n.Link(), source: -1, node: n}
	}
	t := e.newTester()
	t.Batch(e.ctx, "edge", nodes, func(r tester.Result) {
		c := byNode[r.Node]
		if r.OK {
			e.recordOK(c, r)
			e.mu.Lock()
			if s := e.good[c.key]; s != nil {
				s.Source = "edge"
			}
			e.mu.Unlock()
			e.logf("info", "The edge works on port %d (%d ms).", r.Node.Port, r.Delay/time.Millisecond)
		} else {
			e.recordFail(c, r.Err)
		}
	})
}
