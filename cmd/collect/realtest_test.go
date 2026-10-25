package main

import (
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
