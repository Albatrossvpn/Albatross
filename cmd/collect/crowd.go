package main

import (
	"encoding/json"
	"log"
	"os"
	"sort"
	"strconv"
	"time"
)

// crowdStats is what users' apps reported about servers (backend
// list/servers/crowd.json): per server key, tests passed and made from
// their networks. The collector's own test runs abroad and cannot see the
// filtering users face; this can, so servers that work for users come
// first in hot.txt and servers that fail for them are left out of it.
type crowdStats map[uint64][2]float64

// loadCrowd reads the file; a missing or unreadable one is no stats.
func loadCrowd(path string) crowdStats {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		log.Printf("crowd: %v", err)
		return nil
	}
	var f struct {
		Servers map[string][2]float64 `json:"servers"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		log.Printf("crowd: %v", err)
		return nil
	}
	out := crowdStats{}
	for k, v := range f.Servers {
		key, err := strconv.ParseUint(k, 16, 64)
		if err == nil && v[1] > 0 {
			out[key] = v
		}
	}
	log.Printf("crowd: %d servers reported by users", len(out))
	return out
}

// tier ranks a server by users' tests: 0 works for them, 1 unknown or
// mixed, 2 fails for them (enough tests, few passes).
func (c crowdStats) tier(key uint64) int {
	v, ok := c[key]
	if !ok {
		return 1
	}
	ok1, n := v[0], v[1]
	switch {
	case n >= 3 && ok1/n >= 0.5:
		return 0
	case n >= 5 && ok1/n < 0.25:
		return 2
	}
	return 1
}

// crowdOrder sorts passed servers: those users confirmed first, then by
// delay.
func crowdOrder(es []*entry, passed map[*entry]time.Duration, c crowdStats) {
	sort.SliceStable(es, func(i, j int) bool {
		a, b := c.tier(es[i].key), c.tier(es[j].key)
		if a != b {
			return a < b
		}
		return passed[es[i]] < passed[es[j]]
	})
}
