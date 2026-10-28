package engine

import (
	"container/heap"
	"math/rand"
	"net"
	"runtime/debug"
	"sync"
	"time"

	"github.com/Albatrossvpn/vp/internal/antiblock"
	"github.com/Albatrossvpn/vp/internal/geo"
	"github.com/Albatrossvpn/vp/internal/node"
	"github.com/Albatrossvpn/vp/internal/pool"
	"github.com/Albatrossvpn/vp/internal/sources"
	"github.com/Albatrossvpn/vp/internal/tester"
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
	// shardAt is when a long-tail list was last downloaded (shards.go);
	// exhausted, when the scan last ran out of servers to test while none
	// worked, zero since it found some.
	shardAt   time.Time
	exhausted time.Time
}

// protoCodes number the protocols in the pool. Every protocol the user may
// choose needs one: without it a choice tests every protocol.
var protoCodes = map[string]uint8{
	"vmess": 1, "vless": 2, "trojan": 3, "shadowsocks": 4, "hysteria2": 5, "hysteria": 6,
	"tuic": 7, "anytls": 8, "wireguard": 9, "socks": 10, "http": 11, "openvpn": 12, "ssh": 13, "direct": 14,
	"naive": 15, "juicity": 16, "mieru": 17, "brook": 18, "wstunnel": 19, "amneziawg": 20,
}

// refreshEvery says how often each kind of source is downloaded again.
func refreshEvery(s sources.Source, mobile bool) time.Duration {
	switch {
	case s.Relay:
		// The relays inside the country: the list that matters most in a
		// shutdown, small, and served from inside too.
		return 30 * time.Minute
	case (s.Hot || s.Delta) && mobile:
		return 30 * time.Minute
	case s.Hot || s.Delta:
		return 20 * time.Minute
	case shard(s):
		// One at a time, by nextShard.
		_, age := shardTiming(mobile)
		return age
	case s.Central && mobile:
		// new.txt brings the additions in between.
		return 8 * time.Hour
	case s.Central:
		return time.Hour
	case s.Kind == sources.KindTelegram:
		return 20 * time.Minute
	case s.Kind == sources.KindVPNGate, s.Kind == sources.KindLEAP:
		return time.Hour
	case s.Tier == sources.TierDeep:
		// The deep lists are large; their new servers mostly reach the
		// priority lists and Telegram first anyway.
		return 12 * time.Hour
	}
	return 30 * time.Minute
}

// refreshLoop loads cached sources at startup, then keeps downloading them.
func (e *Engine) refreshLoop() {
	defer e.wg.Done()
	states := e.loadStates()
	e.loadListKeys()
	e.warmStart(states)
	e.loadEdge()
	states = nil
	ruleCheck := time.Time{}
	for {
		if time.Since(ruleCheck) > 6*time.Hour {
			ruleCheck = time.Now()
			e.refreshRules()
		}
		e.checkUpdateIf(updateEvery)
		// The network's regime first: a storm of doomed fetches must not
		// delay noticing that foreign addresses are cut.
		if e.regimeDue() {
			e.updateRegime()
		}
		e.refreshDue()
		e.updateHelp()
		if time.Since(e.crowdAt) > crowdEvery && e.proxyClient() != nil {
			// Reports go only through the VPN (crowdReport).
			e.crowdAt = time.Now()
			go e.crowdReport(e.ctx)
		}
		e.mu.Lock()
		// The WARP methods are their own protocol; a chosen one excludes them.
		warp, clean := e.settings.Warp && e.settings.Protocol == "", e.settings.CleanIP
		e.mu.Unlock()
		if clean && e.cf.Age() > 2*time.Hour {
			found := e.cf.Scan(e.ctx, 400, 8)
			if len(found) > 0 {
				e.logf("info", "%d clean edge addresses found (best: %d ms).", len(found), found[0].Latency/time.Millisecond)
			}
		}
		if warp {
			e.refreshWarp()
		}
		e.refreshEdge()
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
				src := srcs[i]
				text, ok := e.cachedText(src)
				if !ok && src.Central && src.Tier == sources.TierPriority {
					// The bundled snapshot is plain text.
					text, ok = sources.SnapshotFor(src), true
					src.Keyed = false
				}
				if ok && text != "" {
					e.ingest(i, src, text, states)
				}
			}
		}()
	}
	// A recent copy of the collector's list already holds what the other
	// built-in sources had; their older copies would only bring back
	// servers the collector dropped. Not one this device cannot open.
	skipBuiltin := false
	for _, s := range srcs {
		if collection(s) && !s.Hot && !s.Delta && time.Since(e.fetcher.CachedAt(s)) < 24*time.Hour {
			if text, ok := e.cachedText(s); ok && opens(s, text) {
				skipBuiltin = true
			}
		}
	}
	_, shardAge := shardTiming(e.opt.Mobile)
	for i, s := range srcs {
		if !s.Enabled || (skipBuiltin && s.Builtin && !s.Central) {
			continue
		}
		if shard(s) && time.Since(e.fetcher.CachedAt(s)) >= shardAge {
			// Ten megabytes of servers the collector could not confirm,
			// days old: not worth the parsing, nor the space.
			e.fetcher.Forget(s)
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
	releaseMemory()
	e.poke()
}

// releaseMemory hands memory freed after parsing large lists back to the
// system right away instead of keeping it for later.
func releaseMemory() { debug.FreeOSMemory() }

// refreshHotNow downloads the hot and new lists at once, for when the
// servers in use stop answering: the collector may already know better
// ones.
func (e *Engine) refreshHotNow() {
	e.mu.Lock()
	for i, s := range e.srcs {
		if (s.Hot || s.Delta) && i < len(e.srcStats) {
			e.srcStats[i].Fetched = time.Time{}
		}
	}
	e.mu.Unlock()
	e.triggerRefill()
}

// refreshDue downloads every source whose refresh time has come. The
// collector's lists go first; while they are reachable and open they stand
// in for every other built-in source, which saves the device hundreds of
// downloads (and Telegram, which is blocked on the direct path anyway).
func (e *Engine) refreshDue() {
	e.refreshSources(true)
	e.refreshSources(false)
}

// centralOK reports whether the collector's list was downloaded lately and
// gave servers: a copy sealed with a key this device lacks (signed out, the
// keys not fetched yet) gives none, and the other lists are needed then.
// Callers hold e.mu.
func (e *Engine) centralOK(now time.Time) bool {
	for i, s := range e.srcs {
		if collection(s) && s.Enabled {
			st := e.srcStats[i]
			if st.LastError == "" && !st.Fetched.IsZero() && now.Sub(st.Fetched) < 6*time.Hour && st.Nodes > 0 {
				return true
			}
		}
	}
	return false
}

// collection says whether s is one of the collector's lists of what it
// collected (hot, new and fresh), not of the project's own servers.
func collection(s sources.Source) bool {
	return s.Central && s.Tier == sources.TierPriority && !s.Sealed
}

// opens says whether this device can open a copy of the list s: a keyed
// one only with its key.
func opens(s sources.Source, text string) bool {
	if !s.Keyed {
		return text != ""
	}
	id, ok := sources.KeyedID(text)
	return ok && haveListKey(id)
}

func (e *Engine) refreshSources(central bool) {
	connected := e.proxyClient() != nil
	e.mu.Lock()
	var due []int
	now := time.Now()
	shut := e.reg.Regime.severity() >= regimeAllowlist.severity() && !connected
	skipBuiltin := !central && (e.centralOK(now) || shut)
	for i, s := range e.srcs {
		if !s.Enabled || s.Central != central || (skipBuiltin && s.Builtin) {
			continue
		}
		if shut && s.Kind == sources.KindTelegram {
			continue // a foreign site; nothing to fetch there now
		}
		st := e.srcStats[i]
		if s.Kind == sources.KindTelegram && !connected && st.LastError != "" && now.Sub(st.Fetched) < 3*time.Hour {
			// t.me is usually blocked on the direct path; wait until the
			// VPN is up instead of stalling every refresh on timeouts.
			continue
		}
		if s.Kind == sources.KindTelegram && connected && st.LastError != "" {
			due = append(due, i)
			continue
		}
		if shard(s) {
			continue
		}
		if now.Sub(st.Fetched) >= refreshEvery(s, e.opt.Mobile) {
			due = append(due, i)
		}
	}
	if central {
		if i := e.nextShard(now, shut); i >= 0 {
			e.scan.shardAt = now
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
					// Retry failed sources sooner than a full interval; not
					// so soon while foreign addresses are cut.
					retry := 10 * time.Minute
					if shut {
						retry = 30 * time.Minute
					}
					st.Fetched = time.Now().Add(-refreshEvery(srcs[i], e.opt.Mobile) + retry)
				}
				e.mu.Unlock()
				if added > 0 {
					e.poke()
				}
			}
		}()
	}
	// Subscriptions first, then Telegram, then the big deep lists.
	order := func(s sources.Source) int {
		switch {
		case s.Tier == sources.TierUser:
			return 0
		case s.Tier == sources.TierPriority && s.Kind != sources.KindTelegram:
			return 1
		case s.Kind == sources.KindTelegram:
			return 2
		}
		return 3
	}
	for rank := 0; rank <= 3; rank++ {
		for _, i := range due {
			if order(srcs[i]) != rank {
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
	// The longest refresh interval is a day; keep two of them. Only the
	// best few thousand are ever tested between refreshes, so no more are
	// kept: servers that worked and the collector's hot list always, then
	// the rest by how they did and how many lists carry them.
	limit := 10000
	if e.opt.Mobile {
		limit = 2000
	}
	e.pool.Compact(48*time.Hour, limit)
	releaseMemory()
	_, untested, _, _ := e.pool.Stats()
	e.logf("info", "Server list updated: %d lists reachable, %d servers not yet tested.", total, untested)
}

func (e *Engine) cachedText(s sources.Source) (string, bool) {
	return e.fetcher.Cached(s)
}

// parseSlots bounds concurrent parsing; a big list briefly holds tens of
// thousands of parsed nodes in memory.
var parseSlots = make(chan struct{}, 2)

// ingest parses a source body and adds its nodes to the pool.
func (e *Engine) ingest(idx int, s sources.Source, text string, states map[uint64][2]uint32) int {
	// A keyed list opens with the list key first; an own list is then
	// still sealed with the build key.
	if s.Keyed {
		if text = e.openKeyed(text); text == "" {
			// No servers came from this copy (centralOK); it is read
			// again once the key is here (rereadKeyed).
			e.mu.Lock()
			e.srcStats[idx].Nodes = 0
			e.mu.Unlock()
			return 0
		}
	}
	if s.Sealed {
		text = sources.Open(text)
	}
	if s.Hot && states == nil {
		// The mark means "in the current hot list", not last week's.
		e.pool.ClearFlag(pool.FlagHot)
	}
	parseSlots <- struct{}{}
	defer func() { <-parseSlots }()
	scores := e.sourceScores()
	scoreOf := func(i uint16) float64 {
		if int(i) < len(scores) {
			return scores[i]
		}
		return 0
	}
	fresh := s.Kind == sources.KindTelegram || s.Tier == sources.TierPriority || s.Tier == sources.TierUser
	added, count := 0, 0
	// Nodes go into the pool as they are parsed; see node.EachText.
	add := func(n *node.Node, hint string) {
		count++
		eng := n.Engine()
		if eng == node.EngineNone {
			return
		}
		if hint == "" {
			hint = geo.Hint(n.Name)
		}
		key := node.Key64(n.Key())
		en := pool.Entry{Key: key, Source: uint16(idx), Proto: protoCodes[n.Proto], Engine: uint8(eng), Style: n.Style(), Net: n.NetID()}
		if len(hint) == 2 {
			en.Hint = [2]byte{hint[0], hint[1]}
		}
		if n.UDPBased() {
			en.Flags |= pool.FlagUDP
		}
		if fresh {
			en.Flags |= pool.FlagFresh
		}
		if s.Hot {
			en.Flags |= pool.FlagHot
		}
		if s.Sealed {
			en.Flags |= pool.FlagOwn
		}
		if e.domesticEndpoint(n, s.Sealed) {
			en.Flags |= pool.FlagDomestic
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
	switch s.Kind {
	case sources.KindVPNGate:
		for _, v := range node.ParseVPNGate(text) {
			add(v.Node, v.Country)
		}
	case sources.KindLEAP:
		for _, v := range node.ParseLEAP(text) {
			add(v.Node, v.Country)
		}
	default:
		node.EachText(text, func(n *node.Node) { add(n, "") })
	}
	e.mu.Lock()
	e.srcStats[idx].Nodes = count
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
	key       uint64
	link      string
	source    int // -1 for servers re-verified from the working list
	node      *node.Node
	good      *Server
	frag      bool   // fragment the TLS ClientHello
	hello     int8   // 1 + index into helloFPs when the app picks the ClientHello
	ech       bool   // look the server's ECH config up over DNS
	pin       string // connect to this IP instead of resolving the name
	pinReason string
	split     string // XHTTP downloads through this second clean edge
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
	ownOK := e.Can(FeatureOwn) // before the lock: it reads the account
	e.mu.Lock()
	srcs := append([]sources.Source(nil), e.srcs...) // read after the lock
	target := e.target
	allowPlain := e.settings.AllowPlain
	protocol := e.settings.Protocol
	wantProto := protoCodes[protocol] // 0 = any
	haveXray := e.bin.Xray != ""
	targetHasServers := false
	// Protocols with a working server now; the others are stocked first,
	// so every protocol the user may choose has a server ready.
	stocked := map[uint8]bool{}
	var recheck []*Server
	for _, s := range e.good {
		if !s.matches(protocol) {
			continue
		}
		if s.usable(now) {
			stocked[protoCodes[s.Proto]] = true
		}
		if target != "" && s.Country == target && s.usable(now) {
			targetHasServers = true
		}
		age := now.Sub(s.TestedAt)
		limit := 30 * time.Minute
		if (target == "" || s.Country == target) && s.usable(now) {
			limit = 10 * time.Minute
		}
		if (s.Own || s.Domestic) && e.wantConnect && e.conn == nil {
			limit = 2 * time.Minute
		}
		if e.conn != nil && e.conn.uses(s.Key) {
			if s.Proto == "wireguard" || s.Proto == "warpplus" {
				// A second session with the same key would take the
				// peer's session away from the live tunnel; the
				// connection's own checks cover it.
				continue
			}
			limit = 5 * time.Minute
		}
		if age > limit && now.Sub(s.LastOK) < 7*24*time.Hour {
			recheck = append(recheck, s)
		}
	}
	e.mu.Unlock()
	scores := e.sourceScores()
	learned := e.net.snapshot()
	e.mu.Lock()
	reg, regProbe := e.reg.Regime, e.reg.Probe
	searching := e.wantConnect && e.conn == nil
	e.mu.Unlock()
	shut := reg.severity() >= regimeAllowlist.severity()
	var out []candidate
	rand.Shuffle(len(recheck), func(i, j int) { recheck[i], recheck[j] = recheck[j], recheck[i] })
	// The project's own servers and the relays inside the country come
	// first, not capped: they are the way out when nothing else is.
	rest := recheck[:0]
	for _, s := range recheck {
		if s.Own || s.Domestic {
			out = append(out, candidate{key: s.key64, link: s.Link, source: -1, good: s})
		} else {
			rest = append(rest, s)
		}
	}
	maxRecheck := len(out) + size/3
	for _, s := range rest {
		if len(out) >= maxRecheck {
			break
		}
		out = append(out, candidate{key: s.key64, link: s.Link, source: -1, good: s})
	}
	_ = searching
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
		if !ownOK && planOwnEntry(en, srcs) {
			// The own servers come with a higher plan.
			return
		}
		if wantProto != 0 && en.Proto != wantProto {
			return
		}
		if en.Engine == 2 && !haveXray {
			return
		}
		if shut && !canGetOut(reg, regProbe, en) {
			// Only what the regime leaves a path for is worth a test.
			return
		}
		sc := 0.0
		if int(en.Source) < len(scores) {
			sc = scores[en.Source] * 100
		}
		sc += float64(min(en.Pop, 10)) * 3
		sc += protoPrior[en.Proto]
		sc += learned.bonus(en.Proto, en.Style)
		sc += learned.netBonus(en.Net)
		sc += reg.bonus(regProbe, en)
		if en.Flags&pool.FlagFresh != 0 {
			sc += 15
		}
		if en.Flags&pool.FlagHot != 0 {
			// Worked from the collector minutes ago.
			sc += 120
		}
		if en.State == pool.StateDead {
			sc -= 40
		}
		if wantProto == 0 && en.Proto != 0 && !stocked[en.Proto] {
			sc += 60
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

// scanPace says how hard to scan right now: how many candidates per batch,
// how many servers one test core holds, and the pause between batches.
// Searching for a server gets everything. Once the chosen country (or the
// world, for the fastest) has plenty of working servers, connected or not,
// scanning drops to a trickle of small batches in small cores.
func (e *Engine) scanPace() (size, chunk int, pause time.Duration) {
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	size, chunk = 300, 150
	switch e.settings.Intensity {
	case "max":
		size = 600
	case "low":
		size = 120
	}
	if e.reg.Regime.severity() >= regimeAllowlist.severity() && e.conn == nil {
		// Only a few candidates can work; the rest would be tested at
		// full pace for nothing, battery and data for nothing.
		return size, chunk, 30 * time.Second
	}
	if e.wantConnect && e.conn == nil {
		return max(size, 300), chunk, 0
	}
	ready := 0
	for _, s := range e.good {
		if s.Country != "IR" && s.usable(now) && s.matches(e.settings.Protocol) && (e.target == "" || s.Country == e.target) {
			ready++
		}
	}
	switch {
	case e.settings.Intensity == "max":
		if e.conn != nil {
			pause = 3 * time.Second
		}
	case ready >= 2*groupSize:
		size, chunk, pause = max(size/5, 24), 50, time.Minute
	case ready >= groupSize:
		size, chunk, pause = size/2, 75, 15*time.Second
	case e.conn != nil:
		pause = 3 * time.Second
	}
	if e.settings.Intensity == "low" {
		pause += 5 * time.Second
	}
	if c := e.conn; c != nil && c.heavy() {
		// The user is moving data: tests would take bandwidth and CPU from
		// it. Keep a trickle so backups still get found.
		size, chunk, pause = max(size/4, 12), max(chunk/4, 6), max(pause, 30*time.Second)
	}
	return size, chunk, pause
}

// scanLoop tests candidates forever.
func (e *Engine) scanLoop() {
	defer e.wg.Done()
	for e.ctx.Err() == nil {
		size, chunk, _ := e.scanPace()
		batch := e.pickBatch(size)
		if len(batch) == 0 {
			e.noneLeft()
			select {
			case <-e.ctx.Done():
				return
			case <-e.wake:
			case <-time.After(20 * time.Second):
			}
			continue
		}
		e.mu.Lock()
		e.scan.exhausted = time.Time{}
		e.mu.Unlock()
		e.runBatch(batch, chunk)
		done := time.Now()
		// Wake-ups (a new target, a connect request, new candidates) end the
		// pause only when the pace they lead to allows it.
		for {
			_, _, pause := e.scanPace()
			left := time.Until(done.Add(pause))
			if left <= 0 {
				break
			}
			select {
			case <-e.ctx.Done():
				return
			case <-e.wake:
			case <-time.After(left):
			}
		}
	}
}

func (e *Engine) runBatch(batch []candidate, chunk int) {
	e.mu.Lock()
	frag := e.settings.Fragment
	cleanOn := e.settings.CleanIP
	free := e.reg.Regime == regimeFree
	spoof := ""
	if e.settings.SpoofSNI != "" && e.opt.IsAdmin() {
		spoof = e.settings.SpoofSNI
	}
	e.mu.Unlock()

	// Parse.
	var cands []candidate
	for _, c := range batch {
		n, err := node.ParseLink(c.link)
		if err != nil || n.Engine() == node.EngineNone {
			e.pool.Update(c.key, func(p *pool.Entry) { p.State = pool.StateRejected })
			e.forget(c.key)
			continue
		}
		c.node = n
		if c.good != nil {
			// As it last worked: ECH, the pinned address and the split edge.
			if c.good.ECH {
				nn := *n
				nn.TryECH = true
				c.node, c.ech = &nn, true
			}
			if c.good.PinIP != "" {
				c.node, c.pin, c.pinReason = antiblock.Pin(c.node, c.good.PinIP), c.good.PinIP, c.good.PinReason
				if c.good.SplitIP != "" {
					c.split = c.good.SplitIP
					c.node.SplitDownload = c.split
				}
			}
		}
		cands = append(cands, c)
	}
	e.mu.Lock()
	e.scan.Testing = len(cands)
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.scan.Testing = 0
		e.mu.Unlock()
	}()

	// Censored resolvers answer blocked names with a fake address; pin
	// those servers to the real one found over DNS-over-HTTPS.
	cands = e.unpoison(cands)

	// TCP probe.
	nodes := make([]*node.Node, len(cands))
	for i := range cands {
		nodes[i] = cands[i].node
	}
	probes := e.tester.TCPProbe(e.ctx, nodes)
	if e.ctx.Err() != nil {
		return // probes cut off by shutdown say nothing about the servers
	}
	var live []candidate
	probed, answered := 0, 0
	for i, c := range cands {
		if !c.node.UDPBased() && !c.node.NoEndpoint() {
			probed++
			if probes[i] >= 0 {
				answered++
			}
		}
		if probes[i] < 0 && !(cleanOn && antiblock.CDNFrontable(c.node)) {
			e.recordFail(c, "unreachable")
			continue
		}
		live = append(live, c)
	}
	if probed >= 50 && answered == 0 {
		// Not one of fifty addresses answered: the network, not the
		// servers, has changed.
		e.reprobeRegime("no server address answers")
	}
	if e.ctx.Err() != nil {
		return
	}
	learned := e.net.snapshot()
	for i := range live {
		c := &live[i]
		c.frag = frag == "on" || (c.good != nil && c.good.Fragment)
		if !helloable(c.node) {
			continue
		}
		if c.good != nil && helloIndex(c.good.Hello) >= 0 {
			c.setHello(helloIndex(c.good.Hello))
			continue
		}
		c.setHello(learned.pickHello(c.frag, rand.Float64))
		if frag == "auto" && !c.frag && c.node.Security == "tls" && c.node.Engine() == node.EngineSingBox && learned.fragHelps(c.fp()) {
			c.frag = true
		}
	}

	// Real tests. Servers that fail get one more chance with the tricks
	// that beat SNI filtering: a fragmented ClientHello and, behind
	// Cloudflare, an edge IP that is not blocked here.
	failed := e.realTest(live, spoof, chunk)
	for _, c := range failed {
		e.net.recordHello(c.helloCode(), false)
	}
	var retry []candidate
	clean := e.cf.Best()
	for _, c := range failed {
		if free {
			// Nothing here needs the tricks that beat filtering.
			break
		}
		r := c
		changed := false
		// The second try changes the ClientHello: fragmented, unless that
		// clearly does worse on this network, else another browser's. ECH
		// and fragmentation are sing-box's; Xray nodes get another
		// browser's hello, which Xray does honour.
		sb := r.node.Engine() == node.EngineSingBox
		switch {
		case sb && !r.ech && r.node.Security == "tls" && r.node.ECH == "" && antiblock.CDNFrontable(r.node):
			// Behind a CDN, ECH hides the name SNI filtering looks for.
			n := *r.node
			n.TryECH = true
			r.node, r.ech, changed = &n, true, true
		case sb && frag == "auto" && !r.frag && r.node.Security == "tls" && !r.node.UDPBased() && (r.hello == 0 || !learned.fragHurts(r.fp())):
			r.frag, changed = true, true
		case r.hello != 0:
			r.setHello(learned.nextHello(r.fp(), r.frag))
			changed = true
		}
		// A clean edge address is tried too, or a fresh one when the address
		// a server was pinned to is no longer among this network's clean
		// ones (another network, or the address got blocked). The pin goes
		// on r.node, so the ClientHello or ECH change above is kept.
		if cleanOn && len(clean) > 0 && antiblock.CDNFrontable(r.node) && (r.pinReason != "clean" || !inClean(clean, r.pin)) {
			i := rand.Intn(min(len(clean), 3))
			ip := clean[i].IP
			r.node, r.pin, r.pinReason, changed = antiblock.Pin(r.node, ip), ip, "clean", true
			if r.node.Transport == "xhttp" && len(clean) > 1 {
				r.split = clean[(i+1)%min(len(clean), 4)].IP
				r.node.SplitDownload = r.split
			}
		}
		if changed {
			retry = append(retry, r)
		}
	}
	if len(retry) > 0 && e.ctx.Err() == nil {
		for _, c := range e.realTest(retry, spoof, chunk) {
			e.net.recordHello(c.helloCode(), false)
			e.recordFail(c, "failed")
		}
	}
	for _, c := range failed {
		if !containsCand(retry, c.key) {
			e.recordFail(c, "failed")
		}
	}
}

func containsCand(list []candidate, key uint64) bool {
	for _, c := range list {
		if c.key == key {
			return true
		}
	}
	return false
}

// unpoison resolves server names and pins nodes whose name the local
// resolver answered with a censor's fake address.
func (e *Engine) unpoison(cands []candidate) []candidate {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 32)
	out := make([]candidate, len(cands))
	drop := make([]bool, len(cands))
	for i, c := range cands {
		out[i] = c
		if c.pin != "" || c.node.NoEndpoint() || net.ParseIP(c.node.Server) != nil {
			continue
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(i int, c candidate) {
			defer func() { <-sem; wg.Done() }()
			r := e.resolver.Lookup(e.ctx, c.node.Server)
			switch {
			case r.Poisoned && r.IP != "":
				out[i].node, out[i].pin, out[i].pinReason = antiblock.Pin(c.node, r.IP), r.IP, "dns"
			case r.Poisoned && e.fetchMode() == sources.ModeNormal:
				drop[i] = true
			}
		}(i, c)
	}
	wg.Wait()
	var keep []candidate
	for i, c := range out {
		if drop[i] {
			e.recordFail(c, "dns blocked")
			continue
		}
		keep = append(keep, c)
	}
	return keep
}

// realTest tests candidates in chunks and returns the ones that failed.
// Rejected and working servers are recorded here; failures are left to
// the caller, which may retry them.
func (e *Engine) realTest(cands []candidate, spoof string, chunk int) []candidate {
	var failed []candidate
	var mu sync.Mutex
	for _, part := range chunks(cands, chunk) {
		if e.ctx.Err() != nil {
			return nil
		}
		byNode := map[*node.Node]candidate{}
		nodes := make([]*node.Node, len(part))
		for i, c := range part {
			nodes[i] = c.node
			byNode[c.node] = c
		}
		t := e.newTester()
		t.Spoof = spoof
		t.FragmentFor = func(n *node.Node) bool { return byNode[n].frag }
		err := t.Batch(e.ctx, "test", nodes, func(r tester.Result) {
			c := byNode[r.Node]
			switch {
			case r.Rejected:
				e.pool.Update(c.key, func(p *pool.Entry) { p.State = pool.StateRejected; p.LastTest = pool.Minutes(time.Now()) })
				e.forget(c.key)
			case r.OK:
				e.recordOK(c, r)
			case r.Tampered && e.distrust(c.key, c.node.Name):
			default:
				mu.Lock()
				failed = append(failed, c)
				mu.Unlock()
			}
		})
		if err != nil && e.ctx.Err() == nil {
			e.logf("warn", "Batch test failed: %v", err)
		}
	}
	return failed
}

// chunks splits candidates so one core holds a bounded number of nodes and
// no two WireGuard nodes with the same key share a core (they would steal
// each other's session).
func chunks(cands []candidate, size int) [][]candidate {
	var out [][]candidate
	rest := cands
	for len(rest) > 0 {
		var cur, later []candidate
		keys := map[string]bool{}
		for _, c := range rest {
			k := ""
			if c.node.Proto == "wireguard" {
				k = c.node.PrivateKey
			}
			if len(cur) >= size || (k != "" && keys[k]) {
				later = append(later, c)
				continue
			}
			if k != "" {
				keys[k] = true
			}
			cur = append(cur, c)
		}
		out = append(out, cur)
		rest = later
	}
	return out
}

func (e *Engine) recordOK(c candidate, r tester.Result) {
	now := time.Now()
	n := c.node
	e.net.record(protoCodes[n.Proto], n.Style(), true)
	e.net.recordHello(c.helloCode(), true)
	e.net.recordNet(n.NetID(), true)
	e.net.recordServer(c.key, true)
	own := false
	e.pool.Update(c.key, func(p *pool.Entry) {
		p.State = pool.StateOK
		p.Fails = 0
		p.LastTest = pool.Minutes(now)
		own = p.Flags&pool.FlagOwn != 0
	})
	domestic := e.domesticEndpoint(n, own)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.httpsOK = now
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
	s.Fragment = c.frag
	s.ECH = c.ech
	s.Domestic, s.Own = domestic, own || s.Own
	s.Hello = ""
	if c.hello != 0 {
		s.Hello = helloFPs[c.fp()]
	}
	s.PinIP, s.PinReason, s.SplitIP = c.pin, c.pinReason, c.split
	e.scan.Tested++
	e.scan.recent = append(e.scan.recent, now)
}

func (e *Engine) recordFail(c candidate, reason string) {
	now := time.Now()
	// Where foreign addresses are cut, a foreign server's failure says
	// nothing about it: nothing is learned from it and it keeps its
	// chances for when they come back. Only what the regime leaves a path
	// for is judged.
	e.mu.Lock()
	reg, p := e.reg.Regime, e.reg.Probe
	e.mu.Unlock()
	frozen := false
	if en, ok := e.pool.Get(c.key); ok {
		frozen = reg.severity() >= regimeAllowlist.severity() && !p.Foreign && !canGetOut(reg, p, &en)
		if !frozen {
			e.net.record(en.Proto, en.Style, false)
			e.net.recordNet(en.Net, false)
			e.net.recordServer(c.key, false)
		}
	}
	e.pool.Update(c.key, func(p *pool.Entry) {
		p.LastTest = pool.Minutes(now)
		if frozen {
			return
		}
		p.State = pool.StateDead
		if p.Fails < 255 {
			p.Fails++
		}
	})
	e.mu.Lock()
	defer e.mu.Unlock()
	if c.source >= 0 && c.source < len(e.srcStats) {
		e.srcStats[c.source].Tested++
	}
	if s := e.good[c.key]; s != nil {
		s.TestedAt = now
		if !frozen {
			s.Fails++
			if s.Fails >= 6 && now.Sub(s.LastOK) > 24*time.Hour {
				delete(e.good, c.key)
			}
		}
	}
	e.scan.Tested++
	e.scan.recent = append(e.scan.recent, now)
}

// canGetOut reports whether a candidate has a path where the regime cuts
// foreign addresses: a relay inside the country, the DNS tunnel while the
// resolver recurses, the ping carrier while ping gets out, UDP 53 while it
// is open, and under the allowlist CDN-fronted TLS servers too.
func canGetOut(r regime, p regimeProbe, en *pool.Entry) bool {
	switch {
	case en.Flags&pool.FlagDomestic != 0:
		return true
	case node.StyleDNSTunnel(en.Style):
		return p.Recursion
	case node.StyleICMP(en.Style):
		return p.ICMP != "no"
	case node.StyleDNSPort(en.Style):
		return p.UDP53
	}
	if r == regimeAllowlist {
		wire, port := node.StyleWire(en.Style), node.StylePort(en.Style)
		tr := (wire - 1) % 8
		return (tr == 1 || tr == 2 || tr == 4 || tr == 5) && port >= 1 && port <= 3
	}
	return false
}

// forget drops a known server the core refuses for good (or whose link
// no longer parses), so it is neither offered nor rechecked again.
func (e *Engine) forget(key uint64) {
	e.mu.Lock()
	s := e.good[key]
	delete(e.good, key)
	e.mu.Unlock()
	// After the lock: logf takes it too.
	if s != nil {
		e.logf("info", "Server %s is no longer usable here and was removed.", s.Name)
	}
}

// statsLoop keeps derived numbers fresh and saves state regularly.
func (e *Engine) statsLoop() {
	defer e.wg.Done()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for n := 1; ; n++ {
		select {
		case <-e.ctx.Done():
			return
		case <-tick.C:
		}
		if n%24 == 0 {
			e.saveServers()
			e.saveSourceStats()
			e.saveStates()
		}
		// Counting a large pool is the costliest thing here; nobody sees the
		// numbers while the window is closed, so do it once a minute then.
		e.mu.Lock()
		watched := time.Since(e.uiSeen) < 3*time.Second
		e.mu.Unlock()
		if !watched && n%12 != 0 {
			continue
		}
		total, untested, dead, okN := e.pool.Stats()
		e.mu.Lock()
		e.poolStats = [4]int{total, untested, dead, okN}
		cut := time.Now().Add(-time.Minute)
		i := 0
		for i < len(e.scan.recent) && e.scan.recent[i].Before(cut) {
			i++
		}
		e.scan.recent = e.scan.recent[i:]
		e.mu.Unlock()
	}
}

// inClean says whether ip is one of the clean edge addresses found here.
func inClean(clean []antiblock.CleanIP, ip string) bool {
	for _, c := range clean {
		if c.IP == ip {
			return true
		}
	}
	return false
}
