package main

import (
	"context"
	"log"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/cezomagoanlace29861-jpg/vp/internal/core"
	"github.com/cezomagoanlace29861-jpg/vp/internal/node"
	"github.com/cezomagoanlace29861-jpg/vp/internal/tester"
)

// realTest connects through the first n entries with the real cores, the
// way the app does, and reports which worked and how fast. A TCP answer
// alone says little: most such servers no longer complete a handshake, and
// phones spent minutes finding that out themselves. It needs the cores
// named by SING_BOX (and XRAY for Xray-only servers); without them it tests
// nothing.
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
	byNode := map[*node.Node]*entry{}
	tested = map[*entry]bool{}
	var nodes []*node.Node
	for _, e := range list {
		if len(nodes) >= n {
			break
		}
		nd, err := node.ParseLink(e.link)
		if err != nil || nd.Proto == "warpplus" {
			continue
		}
		byNode[nd] = e
		tested[e] = true
		nodes = append(nodes, nd)
	}
	passed = map[*entry]time.Duration{}
	var mu sync.Mutex
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
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
	log.Printf("real test: %d of %d servers completed a connection", len(passed), len(nodes))
	return passed, tested
}

// orderFresh puts the servers that passed first, fastest first, keeps the
// untested ones after them, and moves those that failed to rest.
func orderFresh(fresh []*entry, passed map[*entry]time.Duration, tested map[*entry]bool) (keep, failed []*entry) {
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
	sort.SliceStable(ok, func(i, j int) bool { return passed[ok[i]] < passed[ok[j]] })
	return append(ok, untested...), failed
}
