package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Albatrossvpn/vp/internal/antiblock"
	"github.com/Albatrossvpn/vp/internal/core"
	"github.com/Albatrossvpn/vp/internal/node"
	"github.com/Albatrossvpn/vp/internal/tester"
)

// A server passes only when it also carries a real download (speedBytes)
// at speedMin or more, in each of its two tests: answering a small request
// proves little. Overloaded free servers answer and then crawl, and those
// that crawl from a data centre crawl worse for users behind Iran's
// filtering. The bar is high on purpose: the lists the apps try first hold
// only servers that carry.
//
// strict then keeps the servers that did twice as well (speedGood) and
// answered quickly (maxDelay); those between the two bars only fill the
// lists when too few cleared the higher one.
var (
	speedMin   float64 = 256 << 10
	speedBytes         = 2 << 20
)

const maxDelay = 2500 * time.Millisecond

// speedGood is the bar a server must clear to be kept without question.
func speedGood() float64 { return 2 * speedMin }

// strict keeps of what passed both tests the servers that carried at least
// speedGood and answered within maxDelay (a server that is not speed tested,
// a DNS tunnel or Tor, needs only the quick answer). When fewer than keep
// cleared that, the fastest of the rest fill up to keep, so a bad day leaves
// the lists short of nothing; the others are dropped.
func strict(passed map[*entry]time.Duration, keep int) map[*entry]time.Duration {
	if speedMin <= 0 {
		return passed
	}
	out := map[*entry]time.Duration{}
	var rest []*entry
	for e, d := range passed {
		exempt := false
		if n, err := node.ParseLink(e.link); err == nil {
			exempt = tester.SpeedExempt(n)
		}
		if d <= maxDelay && (e.speed >= speedGood() || (exempt && e.speed == 0)) {
			out[e] = d
		} else {
			rest = append(rest, e)
		}
	}
	good := len(out)
	sort.Slice(rest, func(i, j int) bool {
		if rest[i].speed != rest[j].speed {
			return rest[i].speed > rest[j].speed
		}
		return passed[rest[i]] < passed[rest[j]]
	})
	for _, e := range rest {
		if len(out) >= keep {
			break
		}
		out[e] = passed[e]
	}
	log.Printf("strict: %d of %d servers cleared the higher bar; %d kept in all", good, len(passed), len(out))
	return out
}

// batchSeq names each test core's files apart: cores started side by side
// with one name would write one config file, and one of them start with
// the other's servers.
var batchSeq atomic.Int64

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
			if err != nil || nd.Proto == "warpplus" || nd.Engine() == node.EngineSidecar {
				// Sidecar protocols need helpers the collector lacks:
				// they stay untested rather than failing.
				continue
			}
			byNode[nd] = e
			tested[e] = true
			nodes = append(nodes, nd)
		}
		// Several cores at once, each holding a bounded share of the servers.
		const perCore, cores = 120, 6
		sem := make(chan struct{}, cores)
		var wg sync.WaitGroup
		for lo := 0; lo < len(nodes); lo += perCore {
			part := nodes[lo:min(lo+perCore, len(nodes))]
			sem <- struct{}{}
			wg.Add(1)
			go func(part []*node.Node) {
				defer func() { <-sem; wg.Done() }()
				t := &tester.Tester{Runtime: rt, Concurrency: 64, Timeout: 8 * time.Second}
				if speedMin > 0 {
					t.SpeedBytes, t.SpeedTimeout, t.SpeedNeed, t.SpeedConcurrency = speedBytes, 12*time.Second, speedMin, 8
				}
				name := fmt.Sprintf("collect-%d", batchSeq.Add(1))
				err := t.Batch(ctx, name, part, func(r tester.Result) {
					switch {
					case r.OK && !r.Tampered && r.SpeedTested && r.Speed < speedMin:
						// Answers, but does not carry: failed.
					case r.OK && !r.Tampered:
						mu.Lock()
						e := byNode[r.Node]
						passed[e] = r.Delay
						if r.SpeedTested && (e.speed == 0 || r.Speed < e.speed) {
							e.speed = r.Speed
						}
						mu.Unlock()
					case r.Err == "the core could not start":
						// Nothing was learned about the server; it stays
						// in fresh.txt rather than counting as failed.
						mu.Lock()
						delete(tested, byNode[r.Node])
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
	// Every protocol that has passing servers is in the hot list, with at
	// least its reserve of them, so a user who picks any protocol, or a
	// network that spares only one, finds servers.
	return keepReserves(out, cands, max, func(p string) int { return reserveOf(p, max) })
}

// protoReserve is how many servers of each protocol a 300-server hot list
// carries at least when that many passed; wgReserve the same for WireGuard.
const protoReserve, wgReserve = 20, 40

// reserveOf is how many servers of proto a hot list of max servers keeps at
// least: WireGuard more, as it is fast where the filtering lets it through;
// scaled to the list, so a small one (tests) keeps its shares.
func reserveOf(proto string, max int) int {
	if proto == "wireguard" {
		return min(wgReserve, max/7)
	}
	return min(protoReserve, max/15)
}

// keepReserves returns out (at most max servers, best first) holding up to
// reserve(p) servers of every protocol p in pool, taken in pool's order. A
// server added replaces the last one in out of a protocol holding more
// than its own reserve, so no protocol's share is given up for another's,
// and joins at the end.
func keepReserves(out, pool []*entry, max int, reserve func(string) int) []*entry {
	out = append([]*entry(nil), out...)
	in := map[*entry]bool{}
	count := map[string]int{}
	for _, e := range out {
		in[e] = true
		count[e.proto]++
	}
	for _, e := range pool {
		if in[e] || count[e.proto] >= reserve(e.proto) {
			continue
		}
		if len(out) >= max {
			i := len(out) - 1
			for i >= 0 && count[out[i].proto] <= reserve(out[i].proto) {
				i--
			}
			if i < 0 {
				continue
			}
			count[out[i].proto]--
			delete(in, out[i])
			out = append(out[:i], out[i+1:]...)
		}
		out = append(out, e)
		in[e] = true
		count[e.proto]++
	}
	return out
}

// fitHot cuts the hot list to max servers once the probes inside the
// country have reordered it, keeping every protocol's reserve: servers
// without an answer from inside, all the UDP ones among them, sort last,
// where a plain cut falls.
func fitHot(hot []*entry, max int) []*entry {
	return keepReserves(hot[:min(len(hot), max)], hot, max, func(p string) int { return reserveOf(p, max) })
}

// confirm tests the servers that passed once more and keeps only those that
// pass twice, download and all, with the mean of both delays: one lucky
// connection does not make a hot server. A server the second test could not
// reach (its core did not start) is tested once more; one that still has
// no second result is left out, not kept on its first.
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
	var retry []*entry
	for _, e := range again {
		if !tested[e] {
			retry = append(retry, e)
		}
	}
	if len(retry) > 0 {
		third, _ := realTest(retry, len(retry))
		for e, d := range third {
			second[e] = d
		}
	}
	out := map[*entry]time.Duration{}
	for e, d := range passed {
		if d2, ok := second[e]; ok {
			out[e] = (d + d2) / 2
		}
	}
	log.Printf("confirmation: %d of %d servers passed a second time (%d needed a third try)", len(out), len(passed), len(retry))
	return out
}

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
