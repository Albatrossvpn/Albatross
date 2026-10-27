package engine

import (
	"time"

	"github.com/Albatrossvpn/vp/internal/sources"
)

// The collector's long tail, the all-N.txt lists, holds the few hundred
// thousand servers it found but could not confirm: about ten megabytes a
// list, far more than the pool keeps. It is read one list at a time, the
// one read longest ago: on a computer one every few hours; on a phone,
// whose data costs, only while nothing else works.

// shard says whether s is one of the collector's long-tail lists.
func shard(s sources.Source) bool {
	return s.Central && s.Tier == sources.TierDeep
}

// shardTiming says how long to wait between two long-tail downloads, and
// how old a list's copy must be to be downloaded again.
func shardTiming(mobile bool) (gap, age time.Duration) {
	if mobile {
		return 30 * time.Minute, 24 * time.Hour
	}
	return 6 * time.Hour, sources.CentralShards * 6 * time.Hour
}

// nextShard returns the long-tail list to download now, or -1. Callers
// hold e.mu.
func (e *Engine) nextShard(now time.Time, shut bool) int {
	gap, age := shardTiming(e.opt.Mobile)
	if shut || (!e.scan.shardAt.IsZero() && now.Sub(e.scan.shardAt) < gap) {
		// Foreign servers are no use while foreign addresses are cut.
		return -1
	}
	if e.opt.Mobile && e.scan.exhausted.IsZero() {
		return -1
	}
	best := -1
	for i, s := range e.srcs {
		if !shard(s) || !s.Enabled || i >= len(e.srcStats) {
			continue
		}
		at := e.srcStats[i].Fetched
		if !at.IsZero() && now.Sub(at) < age {
			continue
		}
		if best < 0 || at.Before(e.srcStats[best].Fetched) {
			best = i
		}
	}
	return best
}

// noneLeft notes that the scan found nothing left to test. With no server
// working, the long tail is wanted (nextShard).
func (e *Engine) noneLeft() {
	now := time.Now()
	e.mu.Lock()
	working := false
	for _, s := range e.good {
		if s.usable(now) && s.matches(e.settings.Protocol) {
			working = true
			break
		}
	}
	first := !working && e.scan.exhausted.IsZero()
	switch {
	case working:
		e.scan.exhausted = time.Time{}
	case first:
		e.scan.exhausted = now
	}
	e.mu.Unlock()
	if first {
		e.triggerRefill()
	}
}
