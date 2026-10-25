package main

import (
	"strings"
	"testing"
	"time"
)

func TestOrderFresh(t *testing.T) {
	slow, fast, bad, later := &entry{link: "slow"}, &entry{link: "fast"}, &entry{link: "bad"}, &entry{link: "later"}
	passed := map[*entry]time.Duration{slow: 900 * time.Millisecond, fast: 200 * time.Millisecond}
	tested := map[*entry]bool{slow: true, fast: true, bad: true}
	keep, failed := orderFresh([]*entry{bad, slow, later, fast}, passed, tested, nil)
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
	if got := hotList([]*entry{a1, a2, u, b}, passed, 10, nil); len(got) != 2 || got[0] != a1 || got[1] != u {
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
	got := hotList(es, passed, 8, nil)
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
	crowd := crowdStats{1: {0, 6}, 6: {5, 5}}
	got = hotList(es, passed, 3, crowd)
	for _, e := range got {
		if e.key == 1 {
			t.Fatal("a server failing for users stayed hot")
		}
	}
	ok := []*entry{es[0], es[1], es[5]}
	crowdOrder(ok, passed, crowd)
	if ok[0].key != 6 || ok[1].key != 2 || ok[2].key != 1 {
		t.Fatalf("order %v %v %v", ok[0].key, ok[1].key, ok[2].key)
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
