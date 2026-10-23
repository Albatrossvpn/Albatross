package main

import (
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
