package main

import (
	"context"
	"log"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/Albatrossvpn/vp/internal/core"
	"github.com/Albatrossvpn/vp/internal/node"
	"github.com/Albatrossvpn/vp/internal/tester"
)

// realTest connects through up to n of list with the real cores, the way
// the app does, and reports which worked and how fast. A TCP answer alone
// says little: most such servers no longer complete a handshake, and
// phones spent minutes finding that out themselves. It needs the cores
// named by SING_BOX (and XRAY for Xray-only servers); without them it tests
// nothing.
//
// One config per address goes first: an address that is down or blocked
// fails every config on it, so testing it once leaves the budget for more
// addresses. Configs sharing an address that passed are tested with what
// is left; those sharing one that failed count as failed.
func realTest(list []*entry, n int) (passed map[*entry]time.Duration, tested map[*entry]bool) {
	sb := os.Getenv("SING_BOX")
	if sb == "" || n <= 0 {
		return nil, nil
	}
	dir, err := os.MkdirTemp("", "collect-cores")
	if err != nil {
		log.Printf("real test skipped: %v", err)
		return nil, nil
	}
	defer os.RemoveAll(dir)
	rt := &core.Runtime{Bin: core.Binaries{SingBox: sb, Xray: os.Getenv("XRAY")}, Dir: dir}
	reps, siblings := byAddress(list)
	passed = map[*entry]time.Duration{}
	tested = map[*entry]bool{}
	var mu sync.Mutex
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	run := func(es []*entry) {
		byNode := map[*node.Node]*entry{}
		var nodes []*node.Node
		for _, e := range es {
			nd, err := node.ParseLink(e.link)
			if err != nil || nd.Proto == "warpplus" {
				continue
			}
			byNode[nd] = e
			tested[e] = true
			nodes = append(nodes, nd)
		}
		// Several cores at once, each holding a bounded share of the servers.
		const perCore, cores = 250, 4
		sem := make(chan struct{}, cores)
		var wg sync.WaitGroup
		for lo := 0; lo < len(nodes); lo += perCore {
			part := nodes[lo:min(lo+perCore, len(nodes))]
			sem <- struct{}{}
			wg.Add(1)
			go func(part []*node.Node) {
				defer func() { <-sem; wg.Done() }()
				t := &tester.Tester{Runtime: rt, Concurrency: 64, Timeout: 8 * time.Second}
				err := t.Batch(ctx, "collect", part, func(r tester.Result) {
					if r.OK && !r.Tampered {
						mu.Lock()
						passed[byNode[r.Node]] = r.Delay
						mu.Unlock()
					}
				})
				if err != nil && ctx.Err() == nil {
					log.Printf("batch: %v", err)
				}
			}(part)
		}
		wg.Wait()
	}
	if len(reps) > n {
		reps = reps[:n]
	}
	run(reps)
	var more []*entry
	for _, r := range reps {
		if _, ok := passed[r]; ok {
			more = append(more, siblings[r]...)
		} else if tested[r] {
			for _, s := range siblings[r] {
				tested[s] = true
			}
		}
	}
	if left := n - len(reps); left > 0 {
		run(more[:min(left, len(more))])
	}
	log.Printf("real test: %d of %d addresses, %d servers in all completed a connection", countIn(reps, passed), len(reps), len(passed))
	return passed, tested
}

// byAddress returns the first config of each address, in list order, and
// the later configs on the same address. UDP servers (no TCP endpoint)
// each stand alone.
func byAddress(list []*entry) (reps []*entry, siblings map[*entry][]*entry) {
	siblings = map[*entry][]*entry{}
	first := map[string]*entry{}
	for _, e := range list {
		if e.endpoint != "" {
			if r := first[e.endpoint]; r != nil {
				siblings[r] = append(siblings[r], e)
				continue
			}
			first[e.endpoint] = e
		}
		reps = append(reps, e)
	}
	return reps, siblings
}

func countIn(es []*entry, m map[*entry]time.Duration) int {
	k := 0
	for _, e := range es {
		if _, ok := m[e]; ok {
			k++
		}
	}
	return k
}

// hotList is the fastest servers that passed, one per address, for the
// app to try before anything else.
func hotList(ordered []*entry, passed map[*entry]time.Duration, max int, crowd crowdStats) []*entry {
	var cands []*entry
	seen := map[string]bool{}
	for _, e := range ordered {
		if _, ok := passed[e]; !ok || !crowd.keep(e.key) {
			continue
		}
		if e.endpoint != "" {
			if seen[e.endpoint] {
				continue
			}
			seen[e.endpoint] = true
		}
		cands = append(cands, e)
	}
	// Half the list is the best servers in order; the other half gives
	// every protocol its share, so a user who picks any protocol finds
	// servers of it, and a protocol the filtering spares is on hand.
	out := append([]*entry(nil), cands[:min(len(cands), max/2)]...)
	queues := map[string][]*entry{}
	var protos []string
	for _, e := range cands[len(out):] {
		if queues[e.proto] == nil {
			protos = append(protos, e.proto)
		}
		queues[e.proto] = append(queues[e.proto], e)
	}
	for len(out) < max && len(protos) > 0 {
		var left []string
		for _, p := range protos {
			if len(out) >= max {
				break
			}
			q := queues[p]
			out = append(out, q[0])
			if q = q[1:]; len(q) > 0 {
				queues[p] = q
				left = append(left, p)
			}
		}
		protos = left
	}
	return out
}

// confirm tests the servers that passed once more and keeps those that
// pass twice, with the mean of both delays: one lucky connection does not
// make a hot server.
func confirm(passed map[*entry]time.Duration) map[*entry]time.Duration {
	if len(passed) == 0 {
		return passed
	}
	again := make([]*entry, 0, len(passed))
	for e := range passed {
		again = append(again, e)
	}
	sort.Slice(again, func(i, j int) bool { return again[i].link < again[j].link })
	second, tested := realTest(again, len(again))
	if second == nil {
		return passed
	}
	out := map[*entry]time.Duration{}
	for e, d := range passed {
		if d2, ok := second[e]; ok {
			out[e] = (d + d2) / 2
		} else if !tested[e] {
			out[e] = d
		}
	}
	log.Printf("confirmation: %d of %d servers passed a second time", len(out), len(passed))
	return out
}

// checkIran reorders hot by reachability from inside Iran (see iranReach)
// and returns how many addresses were checked and how many were open.
func checkIran(hot []*entry) (checked, open int) {
	var eps []string
	for _, e := range hot {
		if e.endpoint != "" && len(eps) < iranChecks() {
			eps = append(eps, e.endpoint)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	seen := iranReach(ctx, eps, 2)
	for _, ok := range seen {
		if ok {
			open++
		}
	}
	iranFirst(hot, seen)
	log.Printf("globalping: %d of %d hot addresses answer from Iran", open, len(seen))
	return len(seen), open
}

// iranChecks bounds the addresses checked from Iran per run, within the
// hourly quota (two probes each): the anonymous one, or the larger one a
// GLOBALPING_TOKEN gives.
func iranChecks() int {
	if os.Getenv("GLOBALPING_TOKEN") != "" {
		return 500
	}
	return 100
}

// orderFresh puts the servers that passed first, fastest first, keeps the
// untested ones after them, and moves those that failed to rest.
func orderFresh(fresh []*entry, passed map[*entry]time.Duration, tested map[*entry]bool, crowd crowdStats) (keep, failed []*entry) {
	var ok, untested []*entry
	for _, e := range fresh {
		_, good := passed[e]
		switch {
		case good:
			ok = append(ok, e)
		case tested[e]:
			failed = append(failed, e)
		default:
			untested = append(untested, e)
		}
	}
	crowdOrder(ok, passed, crowd)
	return append(ok, untested...), failed
}
