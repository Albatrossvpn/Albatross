package main

import (
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func mkEntries(n int, prefix string) []*entry {
	out := make([]*entry, n)
	for i := range out {
		out[i] = &entry{link: prefix + strconv.Itoa(i), pop: 1}
	}
	return out
}

func TestClassify(t *testing.T) {
	cases := []struct {
		st    srcStat
		known bool
		want  laneKind
	}{
		{srcStat{}, false, laneProbation},
		{srcStat{Tested: 40, Passed: 2}, true, laneProbation},
		{srcStat{Tested: 90, Passed: 5}, true, laneProven},
		{srcStat{Tested: 300, Passed: 0}, true, laneDud},
		{srcStat{Tested: 299, Passed: 0}, true, laneProbation},
		{srcStat{Tested: 600, Passed: 3}, true, laneNormal},
	}
	for _, c := range cases {
		if got := classify(c.st, c.known); got != c.want {
			t.Errorf("classify(%+v, %v) = %v, want %v", c.st, c.known, got, c.want)
		}
	}
}

// A new source puts a bounded random sample forward, a dud costs nothing, and
// the total stays bounded.
func TestPlanLanes(t *testing.T) {
	ids := []string{"new", "proven", "dud", "normal", "gone"}
	per := [][]*entry{mkEntries(5000, "n"), mkEntries(4000, "p"), mkEntries(800, "d"), mkEntries(100, "x"), nil}
	stats := map[string]srcStat{
		"proven": {Tested: 100, Passed: 9},
		"dud":    {Tested: 500},
		"normal": {Tested: 900, Passed: 2},
	}
	p := planLanes(ids, per, stats, rand.New(rand.NewSource(1)))
	count := func(es []*entry, f func(*entry) bool) (n int) {
		for _, e := range es {
			if f(e) {
				n++
			}
		}
		return
	}
	if n := count(per[0], func(e *entry) bool { return e.lane }); n != probationPerSource {
		t.Errorf("probation put %d servers forward, want %d", n, probationPerSource)
	}
	if n := count(per[1], func(e *entry) bool { return e.lane }); n != provenPerSource {
		t.Errorf("proven put %d servers forward, want %d", n, provenPerSource)
	}
	if n := count(per[2], func(e *entry) bool { return e.lane }); n != 0 {
		t.Errorf("a dud put %d servers forward", n)
	}
	if n := count(per[2], func(e *entry) bool { return e.dud == 1 }); n != 800 {
		t.Errorf("%d servers of the dud source were counted, want 800", n)
	}
	if n := count(per[3], func(e *entry) bool { return e.lane || e.dud > 0 }); n != 0 {
		t.Errorf("a normal source was touched: %d", n)
	}
	if p.counts["probation"] != 1 || p.counts["proven"] != 1 || p.counts["dud"] != 1 || p.counts["normal"] != 1 {
		t.Errorf("counts %v", p.counts)
	}
	// The sample is random: a second plan with another seed picks other servers.
	q := [][]*entry{mkEntries(5000, "n")}
	planLanes([]string{"new"}, q, nil, rand.New(rand.NewSource(2)))
	same := 0
	for i := range q[0] {
		if q[0][i].lane && per[0][i].lane {
			same++
		}
	}
	if same > probationPerSource/2 {
		t.Errorf("two seeds chose %d of the same %d servers", same, probationPerSource)
	}
}

func TestPlanLanesCap(t *testing.T) {
	var ids []string
	var per [][]*entry
	for i := 0; i < 40; i++ {
		ids = append(ids, "s"+strconv.Itoa(i))
		per = append(per, mkEntries(1000, "s"+strconv.Itoa(i)+"-"))
	}
	p := planLanes(ids, per, nil, rand.New(rand.NewSource(3)))
	if p.counts["marked"] != laneProbeCap {
		t.Errorf("marked %d, want the cap %d", p.counts["marked"], laneProbeCap)
	}
}

// The quota is shared out in turns, small sources first, and skips dead servers.
func TestPick(t *testing.T) {
	ids := []string{"big", "small", "tiny"}
	per := [][]*entry{mkEntries(1000, "b"), mkEntries(10, "s"), mkEntries(2, "t")}
	p := planLanes(ids, per, nil, rand.New(rand.NewSource(4)))
	for _, e := range per[1][:3] {
		e.dead = true
	}
	got := p.pick(per, 30)
	if len(got) != 30 {
		t.Fatalf("picked %d, want 30", len(got))
	}
	by := map[string]int{}
	seen := map[*entry]bool{}
	for _, e := range got {
		if e.dead || !e.lane || seen[e] {
			t.Fatalf("bad pick %+v", e)
		}
		seen[e] = true
		by[e.link[:1]]++
	}
	if by["t"] != 2 || by["s"] != 7 || by["b"] != 21 {
		t.Errorf("shares %v, want tiny 2, small 7, big 21", by)
	}
	// With a huge quota every live marked server is taken: 1000 big, 7 small, 2 tiny.
	if all := p.pick(per, 5000); len(all) != 1009 {
		t.Errorf("picked %d with a huge quota, want 1009", len(all))
	}
}

func TestUpdateStatsAndLoad(t *testing.T) {
	a, b := mkEntries(4, "a"), mkEntries(3, "b")
	shared := a[0]
	b[0] = shared
	tested := map[*entry]bool{a[0]: true, a[1]: true, b[1]: true}
	passed := map[*entry]time.Duration{a[0]: time.Second}
	now := time.Unix(1700000000, 0)
	got := updateStats(map[string]srcStat{"old": {Tested: 7, Passed: 1, Runs: 2, First: 5}, "a": {Tested: 10, Passed: 3, Runs: 1, First: 9}},
		[]string{"a", "b", "idle"}, [][]*entry{a, b, nil}, tested, passed, now)
	if st := got["a"]; st.Tested != 12 || st.Passed != 4 || st.Runs != 2 || st.First != 9 || st.Found != 4 {
		t.Errorf("a: %+v", st)
	}
	// The shared server counts for both sources.
	if st := got["b"]; st.Tested != 2 || st.Passed != 1 || st.Runs != 1 || st.First != now.Unix() {
		t.Errorf("b: %+v", st)
	}
	if st := got["old"]; st.Tested != 7 {
		t.Errorf("a source not reached today lost its history: %+v", st)
	}
	if _, ok := got["idle"]; ok {
		t.Error("a source with no servers got an entry")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, statsFile), []byte(`{"x":{"found":3,"tested":4,"passed":2,"runs":1,"first":7}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if st := loadStats(dir)["x"]; st.Tested != 4 || st.Passed != 2 || st.First != 7 {
		t.Errorf("loaded %+v", st)
	}
	if loadStats("") != nil || loadStats(t.TempDir()) != nil {
		t.Error("a missing statistics file should give no statistics")
	}
}
