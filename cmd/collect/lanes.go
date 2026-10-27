package main

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Sources earn the collector's test budget by their results.
//
// A source nobody has measured yet (a newly found one) is on probation: a
// random sample of its servers is probed and connected through first, in a
// share of the real test kept for that, so its servers reach users in the
// next collection instead of waiting in the untested shards. The sample's
// pass rate is then kept in source-stats.json. A source whose
// servers pass is proven and keeps a share of every run; one that was
// connected through hundreds of times without a single pass stops costing
// test budget (its servers still go out in the shards, where the apps test
// them themselves). Nothing is ever removed from the lists by this.
const (
	// graduatePasses is how many passes make a source proven.
	graduatePasses = 5
	// dudTests is how many connection tests without a pass make a source a dud.
	dudTests = 300
	// probationPerSource and provenPerSource bound how many servers of one
	// source are put forward in a run.
	probationPerSource = 1500
	provenPerSource    = 2500
	// laneProbeCap bounds the servers probed first for the lanes.
	laneProbeCap = 30000
	// laneShare is the part of the real test (1 in laneShare) kept for the
	// lanes, shared out fairly among their sources.
	laneShare = 3
)

// srcStat is what the collector has learned about one source.
type srcStat struct {
	Found  int   `json:"found"`  // usable servers in the last run
	Tested int   `json:"tested"` // servers connected through, over all runs
	Passed int   `json:"passed"` // of them that worked, over all runs
	Runs   int   `json:"runs"`
	First  int64 `json:"first"` // unix time of the first run
}

type laneKind int

const (
	laneNormal laneKind = iota
	laneProbation
	laneProven
	laneDud
)

func (k laneKind) String() string {
	return [...]string{"normal", "probation", "proven", "dud"}[k]
}

// classify puts a source in its lane from what is known about it.
func classify(st srcStat, known bool) laneKind {
	switch {
	case known && st.Passed >= graduatePasses:
		return laneProven
	case known && st.Tested >= dudTests && st.Passed == 0:
		return laneDud
	case known && st.Tested >= dudTests:
		// Measured at length and in between: no lane, as before.
		return laneNormal
	}
	return laneProbation
}

// statsFile holds the source statistics beside the lists on the servers
// branch (not in meta.json, which the account service reads on every request).
const statsFile = "source-stats.json"

// loadStats reads the source statistics of the last published run.
func loadStats(prevDir string) map[string]srcStat {
	if prevDir == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(prevDir, statsFile))
	if err != nil {
		return nil
	}
	var m map[string]srcStat
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return m
}

// lanePlan is the outcome of planning the lanes for a run.
type lanePlan struct {
	kinds  []laneKind
	counts map[string]int
}

// planLanes sorts the sources into lanes, marks the servers to probe first
// (entry.lane: a random sample from probation and proven sources) and counts
// in entry.dud how many dud sources carry each server.
func planLanes(ids []string, perSrc [][]*entry, stats map[string]srcStat, rng *rand.Rand) *lanePlan {
	p := &lanePlan{kinds: make([]laneKind, len(ids)), counts: map[string]int{}}
	marked := 0
	for i, id := range ids {
		st, ok := stats[id]
		k := classify(st, ok)
		if len(perSrc[i]) == 0 {
			// Not reached today: no servers, and no change of lane.
			p.kinds[i] = k
			continue
		}
		p.kinds[i] = k
		p.counts[k.String()]++
		switch k {
		case laneDud:
			for _, e := range perSrc[i] {
				e.dud++
			}
		case laneProbation, laneProven:
			limit := probationPerSource
			if k == laneProven {
				limit = provenPerSource
			}
			es := append([]*entry(nil), perSrc[i]...)
			rng.Shuffle(len(es), func(a, b int) { es[a], es[b] = es[b], es[a] })
			for _, e := range es {
				if limit == 0 || marked >= laneProbeCap {
					break
				}
				if !e.lane {
					e.lane = true
					marked++
				}
				limit--
			}
		}
	}
	p.counts["marked"] = marked
	return p
}

// pick chooses up to quota servers for the lanes' share of the real test,
// one source after another in turns so that no big source takes it all, from
// the servers that were marked and answered the probe.
func (p *lanePlan) pick(perSrc [][]*entry, quota int) []*entry {
	var queues [][]*entry
	for i, k := range p.kinds {
		if k != laneProbation && k != laneProven {
			continue
		}
		var q []*entry
		for _, e := range perSrc[i] {
			if e.lane && !e.dead {
				q = append(q, e)
			}
		}
		if len(q) > 0 {
			queues = append(queues, q)
		}
	}
	// Smaller sources first in each round: they have the fewest chances.
	sort.SliceStable(queues, func(a, b int) bool { return len(queues[a]) < len(queues[b]) })
	var out []*entry
	chosen := map[*entry]bool{}
	for len(out) < quota && len(queues) > 0 {
		next := queues[:0]
		for _, q := range queues {
			for len(q) > 0 && chosen[q[0]] {
				q = q[1:]
			}
			if len(q) == 0 {
				continue
			}
			if len(out) < quota {
				chosen[q[0]] = true
				out = append(out, q[0])
				q = q[1:]
			}
			if len(q) > 0 {
				next = append(next, q)
			}
		}
		queues = next
	}
	p.counts["selected"] = len(out)
	return out
}

// updateStats folds this run's connection tests into the statistics: every
// source that carried a tested server is credited with it.
func updateStats(prev map[string]srcStat, ids []string, perSrc [][]*entry, tested map[*entry]bool, passed map[*entry]time.Duration, now time.Time) map[string]srcStat {
	out := make(map[string]srcStat, len(prev)+len(ids))
	for id, st := range prev {
		out[id] = st
	}
	for i, id := range ids {
		if len(perSrc[i]) == 0 {
			continue
		}
		st := out[id]
		if st.First == 0 {
			st.First = now.Unix()
		}
		st.Runs++
		st.Found = len(perSrc[i])
		for _, e := range perSrc[i] {
			if tested[e] {
				st.Tested++
				if _, ok := passed[e]; ok {
					st.Passed++
				}
			}
		}
		out[id] = st
	}
	return out
}
