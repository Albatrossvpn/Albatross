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
	keep, failed := orderFresh([]*entry{bad, slow, later, fast}, passed, tested)
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
	if got := hotList([]*entry{a1, a2, u, b}, passed, 10); len(got) != 2 || got[0] != "a1" || got[1] != "u" {
		t.Fatalf("hot %v", got)
	}
}

func TestNewList(t *testing.T) {
	got := newList([]string{"a", "b", "c", "d"}, []string{"a", "b", "c"}, []string{"b", "x"}, 10)
	if strings.Join(got, ",") != "d,b" {
		t.Fatalf("got %v", got)
	}
}
