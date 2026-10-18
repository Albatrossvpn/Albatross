package engine

import (
	"container/heap"
	"math/rand"
	"path/filepath"
	"sync"
	"time"

	"github.com/cezomagoanlace29861-jpg/vp/internal/geo"
	"github.com/cezomagoanlace29861-jpg/vp/internal/node"
	"github.com/cezomagoanlace29861-jpg/vp/internal/pool"
	"github.com/cezomagoanlace29861-jpg/vp/internal/sources"
	"github.com/cezomagoanlace29861-jpg/vp/internal/tester"
)

// scanState is progress shown in the UI.
type scanState struct {
	Phase        string    `json:"phase"`
	SourcesTotal int       `json:"sources_total"`
	SourcesDone  int       `json:"sources_done"`
	SourcesOK    int       `json:"sources_ok"`
	LastRefresh  time.Time `json:"last_refresh"`
	Tested       int       `json:"tested"`
	Found        int       `json:"found"`
	Testing      int       `json:"testing"`
	recent       []time.Time
}

var protoCodes = map[string]uint8{
	"vmess": 1, "vless": 2, "trojan": 3, "shadowsocks": 4, "hysteria2": 5, "hysteria": 6,
	"tuic": 7, "anytls": 8, "wireguard": 9, "socks": 10, "http": 11, "openvpn": 12,
}

// refreshEvery says how often each kind of source is downloaded again.
func refreshEvery(s sources.Source) time.Duration {
	switch {
	case s.Kind == sources.KindTelegram:
		return 20 * time.Minute
	case s.Kind == sources.KindVPNGate:
		return time.Hour
	case s.Tier == sources.TierDeep:
		return 4 * time.Hour
	}
	return 30 * time.Minute
}

// refreshLoop loads cached sources at startup, then keeps downloading them.
func (e *Engine) refreshLoop() {
	defer e.wg.Done()
	states := e.loadStates()
	e.warmStart(states)
	states = nil
	ruleCheck := time.Time{}
	for {
		if time.Since(ruleCheck) > 6*time.Hour {
			ruleCheck = time.Now()
			go updateRules(e.ctx, filepath.Join(e.dir, "rules"), e.anyClient)
		}
		e.refreshDue()
		e.mu.Lock()
		warp := e.settings.Warp
		e.mu.Unlock()
		if warp {
			e.refreshWarp()
		}
		select {
		case <-e.ctx.Done():
			return
		case <-e.refill:
		case <-time.After(2 * time.Minute):
		}
	}
}

// warmStart feeds the pool from disk caches so the app works offline and
// starts testing immediately.
func (e *Engine) warmStart(states map[uint64][2]uint32) {
	e.mu.Lock()
	srcs := append([]sources.Source(nil), e.srcs...)
	e.scan.Phase = "loading"
	e.mu.Unlock()
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				text, ok := e.cachedText(srcs[i])
				if ok {
					e.ingest(i, srcs[i], text, states)
				}
			}
		}()
	}
	for i, s := range srcs {
		if !s.Enabled {
			continue
		}
		select {
		case jobs <- i:
		case <-e.ctx.Done():
		}
		if e.ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
	e.mu.Lock()
	e.scan.Phase = ""
	e.mu.Unlock()
	e.poke()
}

// refreshDue downloads every source whose refresh time has come.
func (e *Engine) refreshDue() {
	e.mu.Lock()
	var due []int
	now := time.Now()
	for i, s := range e.srcs {
		if !s.Enabled {
			continue
		}
		if now.Sub(e.srcStats[i].Fetched) >= refreshEvery(s) {
			due = append(due, i)
		}
	}
	srcs := append([]sources.Source(nil), e.srcs...)
	e.scan.SourcesTotal, e.scan.SourcesDone, e.scan.SourcesOK = len(due), 0, 0
	if len(due) > 0 {
		e.scan.Phase = "fetching"
	}
	e.mu.Unlock()
	if len(due) == 0 {
		return
	}
	// Priority sources first; within a tier keep list order.
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				res := e.fetcher.Fetch(e.ctx, srcs[i])
				added := 0
				if res.Text != "" {
					added = e.ingest(i, srcs[i], res.Text, nil)
				}
				e.mu.Lock()
				st := &e.srcStats[i]
				e.scan.SourcesDone++
				if res.Err == nil {
					st.Fetched = time.Now()
					st.LastError = ""
					e.scan.SourcesOK++
				} else {
					st.LastError = res.Err.Error()
					// Retry failed sources sooner than a full interval.
					st.Fetched = time.Now().Add(-refreshEvery(srcs[i]) + 10*time.Minute)
				}
				e.mu.Unlock()
				if added > 0 {
					e.poke()
				}
			}
		}()
	}
	for _, tier := range []int{sources.TierUser, sources.TierPriority, sources.TierDeep} {
		for _, i := range due {
			if srcs[i].Tier != tier {
				continue
			}
			select {
			case jobs <- i:
			case <-e.ctx.Done():
			}
		}
	}
	close(jobs)
	wg.Wait()
	e.mu.Lock()
	e.scan.Phase = ""
	e.scan.LastRefresh = time.Now()
	total := e.scan.SourcesOK
	e.mu.Unlock()
	e.pool.NextCycle()
	e.pool.Compact(6)
	e.logf("info", "به‌روزرسانی منابع تمام شد: %d منبع در دسترس، %d سرور در صف آزمایش.", total, e.pool.Len())
}

func (e *Engine) cachedText(s sources.Source) (string, bool) {
	return e.fetcher.Cached(s)
}

// ingest parses a source body and adds its nodes to the pool.
func (e *Engine) ingest(idx int, s sources.Source, text string, states map[uint64][2]uint32) int {
	type item struct {
		n    *node.Node
		hint string
	}
	var items []item
	switch s.Kind {
	case sources.KindVPNGate:
		for _, v := range node.ParseVPNGate(text) {
			items = append(items, item{v.Node, v.Country})
		}
	default:
		nodes, _ := node.ParseText(text)
		for _, n := range nodes {
			items = append(items, item{n, ""})
		}
	}
	scores := e.sourceScores()
	scoreOf := func(i uint16) float64 {
		if int(i) < len(scores) {
			return scores[i]
		}
		return 0
	}
	fresh := s.Kind == sources.KindTelegram || s.Tier == sources.TierPriority || s.Tier == sources.TierUser
	added := 0
	for _, it := range items {
		n := it.n
		eng := n.Engine()
		if eng == node.EngineNone {
			continue
		}
		hint := it.hint
		if hint == "" {
			hint = geo.Hint(n.Name)
		}
		key := node.Key64(n.Key())
		en := pool.Entry{Key: key, Source: uint16(idx), Proto: protoCodes[n.Proto], Engine: uint8(eng)}
		if len(hint) == 2 {
			en.Hint = [2]byte{hint[0], hint[1]}
		}
		if n.UDPBased() {
			en.Flags |= pool.FlagUDP
		}
		if fresh {
			en.Flags |= pool.FlagFresh
		}
		if isPlain(n) {
			en.Flags |= pool.FlagPlain
		}
		if e.pool.Add(en, n.Link(), scoreOf) {
			added++
			if st, ok := states[key]; ok {
				e.pool.Update(key, func(p *pool.Entry) {
					p.LastTest = st[0]
					p.State = uint8(st[1])
					p.Fails = uint8(st[1] >> 8)
				})
			}
		}
	}
	e.mu.Lock()
	e.srcStats[idx].Nodes = len(items)
	e.mu.Unlock()
	return added
}

func isPlain(n *node.Node) bool {
	if n.Security != "" {
		return false
	}
	switch n.Proto {
	case "vless", "trojan", "socks", "http":
		return n.Encryption == "" || n.Encryption == "none"
	case "vmess":
		return n.Method == "none" || n.Method == "zero"
	}
	return false
}

func (e *Engine) sourceScores() []float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]float64, len(e.srcStats))
	for i := range e.srcStats {
		out[i] = e.srcStats[i].score()
	}
	return out
}

// candidate is a server picked for testing.
type candidate struct {
	key    uint64
	link   string
	source int // -1 for servers re-verified from the working list
	node   *node.Node
	good   *Server
}

type scored struct {
	key   uint64
	score float64
}

type minHeap []scored

func (h minHeap) Len() int           { return len(h) }
func (h minHeap) Less(i, j int) bool { return h[i].score < h[j].score }
func (h minHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x any)        { *h = append(*h, x.(scored)) }
func (h *minHeap) Pop() any          { old := *h; x := old[len(old)-1]; *h = old[:len(old)-1]; return x }
func (h minHeap) peekMin() float64   { return h[0].score }
func (h *minHeap) offer(s scored, k int) {
	if h.Len() < k {
		heap.Push(h, s)
	} else if s.score > h.peekMin() {
		(*h)[0] = s
		heap.Fix(h, 0)
	}
}

var protoPrior = map[uint8]float64{
	2:  12, // vless (reality, xhttp, ws)
	5:  10, // hysteria2
	3:  6,  // trojan
	1:  4,  // vmess
	7:  6,  // tuic
	8:  6,  // anytls
	4:  3,  // shadowsocks
	9:  2,  // wireguard
	12: 2,  // openvpn
	10: -4, // socks
}

// pickBatch chooses what to test next: due re-checks of working servers,
// then the most promising untested candidates.
func (e *Engine) pickBatch(size int) []candidate {
	now := time.Now()
	nowMin := pool.Minutes(now)
	e.mu.Lock()
	target := e.target
	allowPlain := e.settings.AllowPlain
	haveXray := e.bin.Xray != ""
	targetHasServers := false
	var recheck []*Server
	for _, s := range e.good {
		if target != "" && s.Country == target && s.usable(now) {
			targetHasServers = true
		}
		age := now.Sub(s.TestedAt)
		limit := 30 * time.Minute
		if (target == "" || s.Country == target) && s.usable(now) {
			limit = 10 * time.Minute
		}
		if e.conn != nil && e.conn.uses(s.Key) {
			limit = 5 * time.Minute
		}
		if age > limit && now.Sub(s.LastOK) < 7*24*time.Hour {
			recheck = append(recheck, s)
		}
	}
	e.mu.Unlock()
	scores := e.sourceScores()
	var out []candidate
	rand.Shuffle(len(recheck), func(i, j int) { recheck[i], recheck[j] = recheck[j], recheck[i] })
	maxRecheck := size / 3
	for _, s := range recheck {
		if len(out) >= maxRecheck {
			break
		}
		out = append(out, candidate{key: s.key64, link: s.Link, source: -1, good: s})
	}
	k := size - len(out)
	var h minHeap
	var tb [2]byte
	if len(target) == 2 {
		tb = [2]byte{target[0], target[1]}
	}
	e.pool.Each(func(en *pool.Entry) {
		switch en.State {
		case pool.StateRejected, pool.StateOK:
			return
		case pool.StateDead:
			// Back off exponentially: 1h, 2h, 4h ... up to two days.
			wait := uint32(60) << min(en.Fails, 5)
			if nowMin-en.LastTest < wait {
				return
			}
		}
		if en.Flags&pool.FlagPlain != 0 && !allowPlain {
			return
		}
		if en.Engine == 2 && !haveXray {
			return
		}
		sc := 0.0
		if int(en.Source) < len(scores) {
			sc = scores[en.Source] * 100
		}
		sc += float64(min(en.Pop, 10)) * 3
		sc += protoPrior[en.Proto]
		if en.Flags&pool.FlagFresh != 0 {
			sc += 15
		}
		if en.State == pool.StateDead {
			sc -= 40
		}
		if tb[0] != 0 {
			if en.Hint == tb {
				if targetHasServers {
					sc += 30
				} else {
					sc += 300
				}
			} else if en.Hint[0] == 0 && !targetHasServers {
				sc += 10
			}
		}
		// A little noise keeps one source from monopolizing a batch.
		sc += rand.Float64() * 4
		h.offer(scored{en.Key, sc}, k)
	})
	for _, s := range h {
		link, ok := e.pool.Link(s.key)
		if !ok {
			continue
		}
		en, _ := e.pool.Get(s.key)
		out = append(out, candidate{key: s.key, link: link, source: int(en.Source)})
	}
	return out
}

// scanLoop tests candidates forever.
func (e *Engine) scanLoop() {
	defer e.wg.Done()
	for e.ctx.Err() == nil {
		size := 300
		e.mu.Lock()
		if e.settings.Intensity == "max" {
			size = 600
		} else if e.settings.Intensity == "low" {
			size = 120
		}
		e.mu.Unlock()
		batch := e.pickBatch(size)
		if len(batch) == 0 {
			select {
			case <-e.ctx.Done():
				return
			case <-e.wake:
			case <-time.After(20 * time.Second):
			}
			continue
		}
		e.runBatch(batch)
		e.mu.Lock()
		connected := e.conn != nil
		low := e.settings.Intensity == "low"
		e.mu.Unlock()
		pause := time.Duration(0)
		if connected {
			pause = 3 * time.Second
		}
		if low {
			pause += 5 * time.Second
		}
		if pause > 0 {
			select {
			case <-e.ctx.Done():
				return
			case <-time.After(pause):
			}
		}
	}
}

func (e *Engine) runBatch(batch []candidate) {
	// Parse.
	var cands []candidate
	for _, c := range batch {
		n, err := node.ParseLink(c.link)
		if err != nil || n.Engine() == node.EngineNone {
			e.pool.Update(c.key, func(p *pool.Entry) { p.State = pool.StateRejected })
			continue
		}
		c.node = n
		cands = append(cands, c)
	}
	e.mu.Lock()
	e.scan.Testing = len(cands)
	frag := e.settings.Fragment
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.scan.Testing = 0
		e.mu.Unlock()
	}()

	// TCP probe.
	nodes := make([]*node.Node, len(cands))
	for i := range cands {
		nodes[i] = cands[i].node
	}
	probes := e.tester.TCPProbe(e.ctx, nodes)
	var live []candidate
	for i, c := range cands {
		if probes[i] < 0 {
			e.recordFail(c, "unreachable")
			continue
		}
		live = append(live, c)
	}
	if e.ctx.Err() != nil {
		return
	}

	// Real tests, in chunks so one core holds a bounded number of nodes.
	retry := e.realTest(live, frag == "on")
	if frag == "auto" && len(retry) > 0 {
		e.realTest(retry, true)
	}
}

// realTest tests candidates and returns TLS ones that failed, which may
// work with fragmentation.
func (e *Engine) realTest(cands []candidate, fragment bool) []candidate {
	const chunk = 150
	var retry []candidate
	var mu sync.Mutex
	for start := 0; start < len(cands); start += chunk {
		if e.ctx.Err() != nil {
			return nil
		}
		part := cands[start:min(start+chunk, len(cands))]
		byNode := map[*node.Node]candidate{}
		nodes := make([]*node.Node, len(part))
		for i, c := range part {
			nodes[i] = c.node
			byNode[c.node] = c
		}
		t := *e.tester
		t.Fragment = fragment
		seen := map[*node.Node]bool{}
		err := t.Batch(e.ctx, "test", nodes, func(r tester.Result) {
			c := byNode[r.Node]
			mu.Lock()
			seen[r.Node] = true
			mu.Unlock()
			switch {
			case r.Rejected:
				e.pool.Update(c.key, func(p *pool.Entry) { p.State = pool.StateRejected; p.LastTest = pool.Minutes(time.Now()) })
			case r.OK:
				e.recordOK(c, r, fragment)
			default:
				if !fragment && r.Node.Security == "tls" && !r.Node.UDPBased() && r.Err != "refused" {
					mu.Lock()
					retry = append(retry, c)
					mu.Unlock()
				}
				e.recordFail(c, r.Err)
			}
		})
		if err != nil && e.ctx.Err() == nil {
			e.logf("warn", "آزمایش گروهی ناموفق بود: %v", err)
		}
	}
	return retry
}

func (e *Engine) recordOK(c candidate, r tester.Result, fragment bool) {
	now := time.Now()
	n := c.node
	e.pool.Update(c.key, func(p *pool.Entry) {
		p.State = pool.StateOK
		p.Fails = 0
		p.LastTest = pool.Minutes(now)
	})
	e.mu.Lock()
	defer e.mu.Unlock()
	if c.source >= 0 && c.source < len(e.srcStats) {
		e.srcStats[c.source].Tested++
		e.srcStats[c.source].OK++
	}
	s := e.good[c.key]
	if s == nil {
		s = &Server{
			Key: n.Key(), key64: c.key, Link: c.link, Name: n.Name, Proto: n.Proto, Transport: n.Transport,
			Security: n.Security, Engine: n.Engine().String(), Plain: isPlain(n),
		}
		if c.source >= 0 && c.source < len(e.srcs) {
			s.Source = e.srcs[c.source].Label()
		}
		e.good[c.key] = s
		e.scan.Found++
	}
	s.node = n
	s.Delay = int(r.Delay / time.Millisecond)
	if r.Country != "" {
		s.Country = r.Country
	}
	s.ExitIP = r.ExitIP
	s.TestedAt, s.LastOK = now, now
	s.Fails = 0
	s.OKCount++
	s.Fragment = fragment
	e.scan.Tested++
	e.scan.recent = append(e.scan.recent, now)
}

func (e *Engine) recordFail(c candidate, reason string) {
	now := time.Now()
	e.pool.Update(c.key, func(p *pool.Entry) {
		p.State = pool.StateDead
		if p.Fails < 255 {
			p.Fails++
		}
		p.LastTest = pool.Minutes(now)
	})
	e.mu.Lock()
	defer e.mu.Unlock()
	if c.source >= 0 && c.source < len(e.srcStats) {
		e.srcStats[c.source].Tested++
	}
	if s := e.good[c.key]; s != nil {
		s.TestedAt = now
		s.Fails++
		if s.Fails >= 6 && now.Sub(s.LastOK) > 24*time.Hour {
			delete(e.good, c.key)
		}
	}
	e.scan.Tested++
	e.scan.recent = append(e.scan.recent, now)
}

// statsLoop keeps derived numbers fresh and saves state regularly.
func (e *Engine) statsLoop() {
	defer e.wg.Done()
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	saves := 0
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-tick.C:
		}
		counts := map[string]int{}
		e.pool.Each(func(en *pool.Entry) {
			if en.Hint[0] == 0 || en.State == pool.StateRejected || (en.State == pool.StateDead && en.Fails > 2) {
				return
			}
			counts[en.HintCode()]++
		})
		e.mu.Lock()
		e.hintCounts = counts
		cut := time.Now().Add(-time.Minute)
		i := 0
		for i < len(e.scan.recent) && e.scan.recent[i].Before(cut) {
			i++
		}
		e.scan.recent = e.scan.recent[i:]
		e.mu.Unlock()
		saves++
		if saves%8 == 0 {
			e.saveServers()
			e.saveSourceStats()
			e.saveStates()
		}
	}
}
