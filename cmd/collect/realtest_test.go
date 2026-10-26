package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestOrderFresh(t *testing.T) {
	slow, fast, bad, later := &entry{link: "slow"}, &entry{link: "fast"}, &entry{link: "bad"}, &entry{link: "later"}
	passed := map[*entry]time.Duration{slow: 900 * time.Millisecond, fast: 200 * time.Millisecond}
	tested := map[*entry]bool{slow: true, fast: true, bad: true}
	keep, failed := orderFresh([]*entry{bad, slow, later, fast}, passed, tested, crowdStats{})
	var got []string
	for _, e := range keep {
		got = append(got, e.link)
	}
	if len(got) != 3 || got[0] != "fast" || got[1] != "slow" || got[2] != "later" {
		t.Fatalf("keep = %v", got)
	}
	if len(failed) != 1 || failed[0] != bad {
		t.Fatalf("failed = %v", failed)
	}
}

func TestByAddressAndHotList(t *testing.T) {
	a1 := &entry{link: "a1", endpoint: "1.1.1.1:443"}
	a2 := &entry{link: "a2", endpoint: "1.1.1.1:443"}
	b := &entry{link: "b", endpoint: "2.2.2.2:443"}
	u := &entry{link: "u"}
	reps, sib := byAddress([]*entry{a1, b, a2, u})
	if len(reps) != 3 || reps[0] != a1 || reps[1] != b || reps[2] != u || len(sib[a1]) != 1 || sib[a1][0] != a2 {
		t.Fatalf("reps %v siblings %v", reps, sib)
	}
	passed := map[*entry]time.Duration{a1: 1, a2: 2, u: 3}
	if got := hotList([]*entry{a1, a2, u, b}, passed, 10, crowdStats{}); len(got) != 2 || got[0] != a1 || got[1] != u {
		t.Fatalf("hot %v", got)
	}
}

func TestHotListSharesProtocols(t *testing.T) {
	// Six vless servers faster than the rest, then two of each other
	// protocol: the first half of the list is the fastest, the second half
	// gives every protocol a turn.
	var es []*entry
	passed := map[*entry]time.Duration{}
	for i := 0; i < 6; i++ {
		e := &entry{link: "v" + string(rune('0'+i)), proto: "vless", key: uint64(i + 1)}
		es = append(es, e)
		passed[e] = time.Duration(i+1) * time.Millisecond
	}
	for _, p := range []string{"hysteria2", "trojan", "shadowsocks"} {
		for i := 0; i < 2; i++ {
			e := &entry{link: p + string(rune('0'+i)), proto: p, key: uint64(100 + len(es))}
			es = append(es, e)
			passed[e] = time.Duration(100+len(es)) * time.Millisecond
		}
	}
	got := hotList(es, passed, 8, crowdStats{})
	if len(got) != 8 {
		t.Fatalf("got %d", len(got))
	}
	count := map[string]int{}
	for _, e := range got {
		count[e.proto]++
	}
	if count["vless"] != 5 || count["hysteria2"] != 1 || count["trojan"] != 1 || count["shadowsocks"] != 1 {
		t.Fatalf("shares %v", count)
	}
	// Users' reports: a server that fails for them is left out, one that
	// works for them comes first.
	crowd := crowdStats{rates: map[uint64][2]float64{1: {0, 6}, 6: {5, 5}}}
	got = hotList(es, passed, 3, crowd)
	for _, e := range got {
		if e.key == 1 {
			t.Fatal("a server failing inside stayed hot")
		}
	}
	ok := []*entry{es[0], es[1], es[5]}
	crowdOrder(ok, passed, crowd)
	if ok[0].key != 6 || ok[1].key != 2 || ok[2].key != 1 {
		t.Fatalf("order %v %v %v", ok[0].key, ok[1].key, ok[2].key)
	}
	// A fresh tester report that confirms enough servers makes hot.txt
	// hold confirmed servers only.
	crowd.strict = true
	if got = hotList(es, passed, 8, crowd); len(got) != 1 || got[0].key != 6 {
		t.Fatalf("strict: %v", got)
	}
}

func TestCrowdFiles(t *testing.T) {
	dir := t.TempDir()
	users := filepath.Join(dir, "crowd.json")
	os.WriteFile(users, []byte(`{"updated":"2026-01-01T00:00:00Z","servers":{"a":[1,3],"b":[0,2]}}`), 0o644)
	var keys []string
	for i := 0; i < strictMin; i++ {
		keys = append(keys, `"`+strconv.FormatUint(uint64(1000+i), 16)+`":[2,2]`)
	}
	iran := filepath.Join(dir, "iran.json")
	os.WriteFile(iran, []byte(`{"updated":"`+time.Now().UTC().Format(time.RFC3339)+`","tester":"iran","servers":{"a":[2,2],`+strings.Join(keys, ",")+`}}`), 0o644)
	c := loadCrowd(users + "," + iran + ",missing.json")
	if !c.strict {
		t.Fatal("a fresh tester report confirming enough servers should be strict")
	}
	if c.tier(0xa) != 0 || c.tier(0xb) != 2 || c.tier(1000) != 0 || c.tier(5) != 1 {
		t.Fatalf("tiers %d %d %d %d", c.tier(0xa), c.tier(0xb), c.tier(1000), c.tier(5))
	}
	if c.keep(5) || !c.keep(1000) {
		t.Fatal("strict keeps confirmed servers only")
	}
	// A stale tester report never makes the list strict.
	os.WriteFile(iran, []byte(`{"updated":"2026-01-01T00:00:00Z","tester":"iran","servers":{`+strings.Join(keys, ",")+`}}`), 0o644)
	if loadCrowd(iran).strict {
		t.Fatal("a stale report was taken as current")
	}
	// The report a tester writes reads back.
	e1, e2, e3 := &entry{key: 1}, &entry{key: 2}, &entry{key: 3}
	first := map[*entry]time.Duration{e1: 1, e2: 1}
	rep := filepath.Join(dir, "rep.json")
	writeReport(rep, "iran", first, map[*entry]time.Duration{e1: 1}, map[*entry]bool{e1: true, e2: true, e3: true})
	r := loadCrowd(rep)
	if r.rates[1] != [2]float64{2, 2} || r.rates[2] != [2]float64{1, 2} || r.rates[3] != [2]float64{0, 1} {
		t.Fatalf("report %v", r.rates)
	}
}

func TestNewList(t *testing.T) {
	got := newList([]string{"a", "b", "c", "d"}, []string{"a", "b", "c"}, []string{"b", "x"}, 10)
	if strings.Join(got, ",") != "d,b" {
		t.Fatalf("got %v", got)
	}
}

func TestIranFirst(t *testing.T) {
	a, b, c := &entry{link: "a", endpoint: "a:1"}, &entry{link: "b", endpoint: "b:1"}, &entry{link: "c", endpoint: "c:1"}
	got := links(iranFirst([]*entry{a, b, c}, map[string]bool{"a:1": false, "c:1": true}))
	if strings.Join(got, "") != "cba" {
		t.Fatalf("got %v", got)
	}
}

func TestDiscoverMentions(t *testing.T) {
	text := `see https://github.com/some/repo/blob/main/sub/all.txt and https://raw.githubusercontent.com/a/b/0123456789abcdef0123456789abcdef01234567/x.yaml?token=1
	join t.me/s/Good_Channel or @another_one, not t.me/joinchat/abc, https://example.com/page.html`
	subs, chans := mentions(text)
	if len(subs) != 2 || subs[0] != "https://raw.githubusercontent.com/some/repo/main/sub/all.txt" || subs[1] != "https://raw.githubusercontent.com/a/b/HEAD/x.yaml" {
		t.Fatalf("subs %v", subs)
	}
	if strings.Join(chans, ",") != "good_channel,another_one,joinchat" {
		t.Fatalf("channels %v", chans)
	}
	if canonical("ftp://x/y.txt") != "" || canonical("https://host/") != "" {
		t.Fatal("non-https or bare addresses must be dropped")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "s.txt"), []byte("# found\nhttps://a/b.txt\n\nhttps://c/d.txt\n"), 0o644)
	got := extraSources(filepath.Join(dir, "s.txt"), "sub")
	if len(got) != 2 || got[1].URL != "https://c/d.txt" || got[0].Tier != 1 {
		t.Fatalf("extra %v", got)
	}
}

// The check from inside the country keeps its answers between runs: with
// fresh answers for every address nothing is asked, what does not answer
// inside is left out, and what does comes first.
func TestCheckIranUsesItsCache(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Unix()
	cache := map[string]iranSeen{
		"a:1": {Open: false, At: now - 60},
		"b:1": {Open: true, At: now - 60},
		"d:1": {Open: true, At: now - int64(iranKeep/time.Second) - 1}, // too old: dropped from the file
	}
	b, _ := json.Marshal(cache)
	os.WriteFile(filepath.Join(dir, iranCacheName), b, 0o644)
	a := &entry{link: "vless://a@a:1#A", endpoint: "a:1"}
	bb := &entry{link: "vless://b@b:1#B", endpoint: "b:1"}
	c := &entry{link: "hysteria2://c@c:1#C"} // no TCP endpoint: never checked, kept
	// Nothing left to ask, and no edge addresses either: no probe is used.
	defer func(n int) { edgeChecks = n }(edgeChecks)
	edgeChecks = 0
	kept, checked, open := checkIran([]*entry{a, bb, c}, dir, dir)
	if got := links(kept); len(got) != 2 || got[0] != bb.link || got[1] != c.link {
		t.Fatalf("kept %v", got)
	}
	if checked != 2 || open != 1 {
		t.Fatalf("checked %d open %d", checked, open)
	}
	saved := loadIranCache(dir)
	if _, ok := saved["d:1"]; ok || len(saved) != 2 {
		t.Fatalf("saved %v", saved)
	}
}

// Servers of a protocol the most shared lists lack are still among the
// ones the real test covers, and the hot list keeps room for WireGuard.
func TestRareProtocolsAreTestedAndKept(t *testing.T) {
	var list []*entry
	for i := 0; i < 50; i++ {
		list = append(list, &entry{link: "vless://x@v" + string(rune('a'+i%26)) + ":1#" + string(rune('A'+i%26)), proto: "vless", pop: 100 - i})
	}
	for i := 0; i < 5; i++ {
		list = append(list, &entry{link: "wireguard://k@w:" + string(rune('1'+i)) + "#W", proto: "wireguard", pop: 1})
	}
	promoteRare(list, 20, 3)
	wg := 0
	for _, e := range list[:20] {
		if e.proto == "wireguard" {
			wg++
		}
	}
	if wg != 3 || len(list) != 55 {
		t.Fatalf("%d wireguard among the first 20, %d entries", wg, len(list))
	}
	if list[0].proto != "vless" || list[0].pop != 100 {
		t.Fatalf("the most shared server moved: %+v", list[0])
	}

	out := list[:4]
	cands := list
	got := reserveProto(append([]*entry(nil), out...), cands, "wireguard", 2)
	wg = 0
	for _, e := range got {
		if e.proto == "wireguard" {
			wg++
		}
	}
	if len(got) != 4 || wg != 2 {
		t.Fatalf("reserve: %d entries, %d wireguard", len(got), wg)
	}
	if got[0] != list[0] {
		t.Fatal("the first server was replaced instead of the last")
	}
}

// Every protocol with passing servers has at least protoReserve in the
// hot list, even when the best half is all one protocol.
func TestHotListHasEveryProtocol(t *testing.T) {
	var ordered []*entry
	passed := map[*entry]time.Duration{}
	add := func(proto string, n int, d time.Duration) {
		for i := 0; i < n; i++ {
			e := &entry{link: proto + "://x@" + proto + string(rune('a'+i/26)) + string(rune('a'+i%26)) + ":1#X", proto: proto, endpoint: proto + string(rune('a'+i/26)) + string(rune('a'+i%26)) + ":1"}
			ordered = append(ordered, e)
			passed[e] = d
		}
	}
	add("vless", 400, time.Millisecond)
	add("trojan", 30, time.Second)
	add("ss", 25, time.Second)
	add("wireguard", 50, time.Second)
	add("tuic", 3, time.Second)
	hot := hotList(ordered, passed, 300, crowdStats{rates: map[uint64][2]float64{}})
	if len(hot) != 300 {
		t.Fatalf("%d hot", len(hot))
	}
	count := map[string]int{}
	for _, e := range hot {
		count[e.proto]++
	}
	if count["trojan"] < protoReserve || count["ss"] < protoReserve || count["wireguard"] < wgReserve || count["tuic"] != 3 {
		t.Fatalf("protocol counts: %v", count)
	}
}
