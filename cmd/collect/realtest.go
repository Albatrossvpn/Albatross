package main

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/Albatrossvpn/vp/internal/antiblock"
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
	// Xray with 250 servers takes a while to come up on a busy runner;
	// given 15 seconds it was given up on, and its servers never tested.
	rt := &core.Runtime{Bin: core.Binaries{SingBox: sb, Xray: os.Getenv("XRAY")}, Dir: dir, StartWait: 60 * time.Second}
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
// checkIran asks probes inside the country which hot addresses answer
// there, keeping the answers in iran-tcp.json (read from prevDir, written
// to outDir) so a run spends its quota on addresses not checked lately.
// It returns the hot list with what does not answer from inside left out,
// those that do first, and how many addresses had an answer and answered.
func checkIran(hot []*entry, prevDir, outDir string) ([]*entry, int, int) {
	cache := loadIranCache(prevDir)
	now := time.Now()
	var eps []string
	for _, e := range hot {
		if e.endpoint == "" {
			continue
		}
		if c, ok := cache[e.endpoint]; ok && now.Sub(time.Unix(c.At, 0)) < iranFresh {
			continue
		}
		if len(eps) < iranChecks() {
			eps = append(eps, e.endpoint)
		}
	}
	// Cloudflare edge addresses too: the apps reach the account service
	// through whichever of them the network lets through (reach.json).
	if edgeChecks > 0 {
		eps = append(eps, edgeCandidates(cache, now)...)
	}
	if len(eps) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		defer cancel()
		for ep, open := range iranReach(ctx, eps, gpProbes()) {
			cache[ep] = iranSeen{Open: open, At: now.Unix()}
		}
	}
	saveIranCache(outDir, cache, now)
	writeReach(outDir, cache, now)
	seen := map[string]bool{}
	checked, open := 0, 0
	for _, e := range hot {
		if c, ok := cache[e.endpoint]; ok && e.endpoint != "" && now.Sub(time.Unix(c.At, 0)) < iranFresh {
			seen[e.endpoint] = c.Open
			checked++
			if c.Open {
				open++
			}
		}
	}
	kept := hot[:0:0]
	for _, e := range hot {
		if o, ok := seen[e.endpoint]; !ok || o {
			kept = append(kept, e)
		}
	}
	iranFirst(kept, seen)
	log.Printf("globalping: %d of %d hot addresses answer from Iran (%d asked this run); %d left out", open, checked, len(eps), len(hot)-len(kept))
	return kept, checked, open
}

// iranSeen is one address's last answer from inside the country.
type iranSeen struct {
	Open bool  `json:"open"`
	At   int64 `json:"at"`
}

// iranFresh is how long an answer from inside counts; iranKeep how long
// it is kept in the file at all.
const iranFresh, iranKeep = 3 * time.Hour, 24 * time.Hour

const iranCacheName = "iran-tcp.json"

func loadIranCache(dir string) map[string]iranSeen {
	out := map[string]iranSeen{}
	if dir == "" {
		return out
	}
	b, err := os.ReadFile(filepath.Join(dir, iranCacheName))
	if err != nil {
		return out
	}
	json.Unmarshal(b, &out)
	return out
}

func saveIranCache(dir string, cache map[string]iranSeen, now time.Time) {
	if dir == "" {
		return
	}
	for ep, c := range cache {
		if now.Sub(time.Unix(c.At, 0)) > iranKeep {
			delete(cache, ep)
		}
	}
	b, err := json.Marshal(cache)
	if err == nil {
		os.WriteFile(filepath.Join(dir, iranCacheName), b, 0o644)
	}
}

// iranChecks bounds the addresses checked from Iran per run, within the
// hourly quota (two probes each): the anonymous one, or the larger one a
// GLOBALPING_TOKEN gives.
func iranChecks() int {
	if os.Getenv("GLOBALPING_TAGS") != "" {
		// Own probes: one test per address, paid for by the credits they earn.
		return 300
	}
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

// edgeAddrs are Cloudflare edge addresses tried first for reach.json:
// cloudflare.com's own and ones widely found open inside the country.
// Beside them, each run has the scan (antiblock.Scanner) pick a few real
// edges at random, so new open ranges are found over time.
var edgeAddrs = []string{"104.16.132.229", "104.16.133.229", "188.114.96.3", "188.114.97.3", "104.16.123.96", "172.64.153.1", "104.21.16.1", "104.26.0.1", "162.159.128.1", "141.101.64.1"}

// edgeChecks is how many edge addresses a run asks the probes inside about
// (a variable so tests can turn the asking off).
var edgeChecks = 30

// edgeCandidates lists edge addresses (as host:443) to check from inside
// this run: the fixed ones and a random sample of real edges, those not
// checked lately first, at most edgeChecks.
func edgeCandidates(cache map[string]iranSeen, now time.Time) []string {
	var out []string
	seen := map[string]bool{}
	add := func(ip string) {
		ep := net.JoinHostPort(ip, "443")
		if seen[ep] {
			return
		}
		seen[ep] = true
		if c, ok := cache[ep]; ok && now.Sub(time.Unix(c.At, 0)) < iranFresh {
			return
		}
		if len(out) < edgeChecks {
			out = append(out, ep)
		}
	}
	for _, ip := range edgeAddrs {
		add(ip)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	for _, c := range (&antiblock.Scanner{}).Scan(ctx, 120, 40) {
		add(c.IP)
	}
	return out
}

// reachFile is reach.json: the edge addresses open from inside, for the
// apps (internal/engine/reach.go).
type reachFile struct {
	Updated    time.Time `json:"updated"`
	Cloudflare []string  `json:"cloudflare"`
}

func writeReach(dir string, cache map[string]iranSeen, now time.Time) {
	if dir == "" {
		return
	}
	var open []string
	for ep, c := range cache {
		host, port, err := net.SplitHostPort(ep)
		if err != nil || port != "443" || !c.Open || now.Sub(time.Unix(c.At, 0)) > iranFresh || !antiblock.IsCloudflare(host) {
			continue
		}
		open = append(open, host)
	}
	sort.Strings(open)
	b, err := json.MarshalIndent(reachFile{Updated: now.UTC(), Cloudflare: open}, "", "  ")
	if err == nil {
		os.WriteFile(filepath.Join(dir, "reach.json"), b, 0o644)
	}
	log.Printf("reach.json: %d Cloudflare edge addresses open from Iran", len(open))
}
