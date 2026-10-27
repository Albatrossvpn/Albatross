// Package pool holds every candidate server found in the sources. Links are
// kept in an append-only file on disk and only a compact record per server
// stays in memory, so hundreds of thousands of candidates cost a few tens
// of megabytes.
package pool

import (
	"bufio"
	"io"
	"os"
	"sort"
	"sync"
	"time"
)

// State of a candidate.
const (
	StateNew      uint8 = iota // never tested
	StateDead                  // unreachable (TCP probe or real test failed)
	StateOK                    // worked at the last test
	StateRejected              // the core refused its config, or it forged a certificate; never tested again
)

// Entry is the in-memory record of one candidate. Keep it small.
type Entry struct {
	Key      uint64
	off      int64
	size     uint32
	Seen     uint32 // refresh cycle that last saw it in a source
	SeenAt   uint32 // unix minutes it was last seen in a source
	LastTest uint32 // unix minutes
	Source   uint16 // index of the best-scoring source that listed it
	Proto    uint8
	Engine   uint8
	State    uint8
	Fails    uint8
	Pop      uint8  // number of sources listing it, saturating
	Style    uint8  // node.Style
	Net      uint32 // node.NetID
	Hint     [2]byte
	Flags    uint8
}

// Entry flags.
const (
	FlagUDP      uint8 = 1 << iota // a TCP probe says nothing about it
	FlagFresh                      // listed by a tested or Telegram source
	FlagPlain                      // no transport encryption
	FlagHot                        // listed in the collector's hot.txt
	FlagDomestic                   // its address is inside the country (a relay)
	FlagOwn                        // from the project's own sealed lists
)

// HintCode returns the country hint as a string.
func (e *Entry) HintCode() string {
	if e.Hint[0] == 0 {
		return ""
	}
	return string(e.Hint[:])
}

// Pool is safe for concurrent use.
type Pool struct {
	mu      sync.RWMutex
	path    string
	f       *os.File
	w       *bufio.Writer
	size    int64
	entries []Entry
	index   map[uint64]int32
	cycle   uint32
	// dropped remembers the test history of candidates Compact removed,
	// so one a list brings back is not retested as if it were new.
	dropped map[uint64]history
}

// history is what is kept of a dropped candidate.
type history struct {
	LastTest uint32
	State    uint8
	Fails    uint8
}

// maxDropped bounds the memory the histories take.
const maxDropped = 50000

// Open creates a fresh pool file at path, replacing any old one.
func Open(path string) (*Pool, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	return &Pool{path: path, f: f, w: bufio.NewWriterSize(f, 1<<16), index: map[uint64]int32{}, cycle: 1}, nil
}

// Close releases the file.
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.w.Flush()
	return p.f.Close()
}

// Cycle is the current refresh cycle number.
func (p *Pool) Cycle() uint32 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.cycle
}

// NextCycle starts a new refresh cycle.
func (p *Pool) NextCycle() uint32 {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cycle++
	return p.cycle
}

// Add records a candidate seen in a source during the current cycle. A new
// key is appended; a known key gets its popularity and source updated.
// It reports whether the key was new.
func (p *Pool) Add(e Entry, link string, sourceScore func(uint16) float64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i, ok := p.index[e.Key]; ok {
		cur := &p.entries[i]
		cur.SeenAt = Minutes(time.Now())
		if cur.Seen != p.cycle {
			cur.Seen = p.cycle
			cur.Pop = 1
		} else if cur.Pop < 255 {
			cur.Pop++
		}
		if sourceScore != nil && sourceScore(e.Source) > sourceScore(cur.Source) {
			cur.Source = e.Source
		}
		cur.Flags |= e.Flags & (FlagFresh | FlagHot | FlagDomestic | FlagOwn)
		if cur.Hint[0] == 0 {
			cur.Hint = e.Hint
		}
		return false
	}
	if h, ok := p.dropped[e.Key]; ok {
		e.LastTest, e.State, e.Fails = h.LastTest, h.State, h.Fails
		delete(p.dropped, e.Key)
	}
	n, err := p.w.WriteString(link)
	if err != nil {
		return false
	}
	p.w.WriteByte('\n')
	e.off = p.size
	e.size = uint32(n)
	p.size += int64(n) + 1
	e.Seen = p.cycle
	e.SeenAt = Minutes(time.Now())
	e.Pop = 1
	p.index[e.Key] = int32(len(p.entries))
	p.entries = append(p.entries, e)
	return true
}

// Len is the number of candidates.
func (p *Pool) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.entries)
}

// Link reads a candidate's link from disk.
func (p *Pool) Link(key uint64) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	i, ok := p.index[key]
	if !ok {
		return "", false
	}
	e := p.entries[i]
	p.w.Flush()
	// Read under the lock: a Compact meanwhile would swap the file and
	// move every offset.
	buf := make([]byte, e.size)
	if _, err := p.f.ReadAt(buf, e.off); err != nil && err != io.EOF {
		return "", false
	}
	return string(buf), true
}

// Get returns a copy of an entry.
func (p *Pool) Get(key uint64) (Entry, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	i, ok := p.index[key]
	if !ok {
		return Entry{}, false
	}
	return p.entries[i], true
}

// Update changes an entry in place.
func (p *Pool) Update(key uint64, fn func(*Entry)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i, ok := p.index[key]; ok {
		fn(&p.entries[i])
	}
}

// Each calls fn for every entry under a read lock. fn must not call back
// into the pool.
func (p *Pool) Each(fn func(*Entry)) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for i := range p.entries {
		fn(&p.entries[i])
	}
}

// Stats counts entries by state.
func (p *Pool) Stats() (total, untested, dead, ok int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for i := range p.entries {
		switch p.entries[i].State {
		case StateNew:
			untested++
		case StateOK:
			ok++
		default:
			dead++
		}
	}
	return len(p.entries), untested, dead, ok
}

// Compact drops candidates that no source has listed for the keep
// duration (unless they are known to work) and rewrites the file. Beyond
// limit candidates (0: no limit), the least promising ones go too: those
// that failed, then those from untested lists, then the rest; within each,
// those on few lists and the longest unseen first. Servers that worked and
// the collector's hot list always stay.
func (p *Pool) Compact(keep time.Duration, limit int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	cutoff := Minutes(time.Now().Add(-keep))
	drop := map[int]bool{}
	for i := range p.entries {
		e := &p.entries[i]
		// A rejected server (a forged certificate, a config the core
		// refuses) is kept so that a list cannot bring it back as new.
		if e.State != StateOK && e.State != StateRejected && e.SeenAt < cutoff {
			drop[i] = true
		}
	}
	if over := len(p.entries) - len(drop) - limit; limit > 0 && over > 0 {
		var cand []int
		for i := range p.entries {
			if e := &p.entries[i]; !drop[i] && e.State != StateOK && e.State != StateRejected && e.Flags&FlagHot == 0 {
				cand = append(cand, i)
			}
		}
		rank := func(e *Entry) (int, uint8, uint32) {
			tier := 2 // from a tested or Telegram list
			if e.State == StateDead {
				tier = 0
			} else if e.Flags&FlagFresh == 0 {
				tier = 1
			}
			return tier, e.Pop, e.SeenAt
		}
		sort.Slice(cand, func(a, b int) bool {
			a1, a2, a3 := rank(&p.entries[cand[a]])
			b1, b2, b3 := rank(&p.entries[cand[b]])
			if a1 != b1 {
				return a1 < b1
			}
			if a2 != b2 {
				return a2 < b2
			}
			return a3 < b3
		})
		for _, i := range cand[:min(over, len(cand))] {
			drop[i] = true
		}
	} else if len(drop)*3 < len(p.entries) {
		return nil
	}
	if len(drop) == 0 {
		return nil
	}
	p.w.Flush()
	tmpPath := p.path + ".compact"
	tmp, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(tmp, 1<<16)
	var kept []Entry
	index := map[uint64]int32{}
	var size int64
	for i := range p.entries {
		e := p.entries[i]
		if drop[i] {
			continue
		}
		buf := make([]byte, e.size)
		if _, err := p.f.ReadAt(buf, e.off); err != nil && err != io.EOF {
			continue
		}
		w.Write(buf)
		w.WriteByte('\n')
		e.off = size
		size += int64(e.size) + 1
		index[e.Key] = int32(len(kept))
		kept = append(kept, e)
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	tmp.Close()
	old := p.f
	old.Close()
	// Renamed before it is opened: Windows refuses to rename a file that
	// is open (Go opens files without FILE_SHARE_DELETE), and every
	// compaction there failed, so the pool grew without bound.
	if err := os.Rename(tmpPath, p.path); err != nil {
		// The old file is intact where it was: keep it, the entries and
		// the index as they were, so the pool goes on working.
		os.Remove(tmpPath)
		if of, oerr := os.OpenFile(p.path, os.O_RDWR, 0o644); oerr == nil {
			of.Seek(0, io.SeekEnd)
			p.f, p.w = of, bufio.NewWriterSize(of, 1<<16)
		}
		return err
	}
	f, err := os.OpenFile(p.path, os.O_RDWR, 0o644)
	if err != nil {
		// The compacted file is in place but cannot be opened: the entries
		// follow it, and writes fail until the next start reopens it.
		p.entries, p.index, p.size = kept, index, size
		return err
	}
	// The history of what was dropped (tested entries only) outlives the
	// drop, so a list bringing one back does not make it new again.
	if p.dropped == nil {
		p.dropped = map[uint64]history{}
	}
	for i := range p.entries {
		if e := &p.entries[i]; drop[i] && e.State != StateNew && len(p.dropped) < maxDropped {
			p.dropped[e.Key] = history{LastTest: e.LastTest, State: e.State, Fails: e.Fails}
		}
	}
	f.Seek(0, io.SeekEnd)
	p.f, p.w, p.size, p.entries, p.index = f, bufio.NewWriterSize(f, 1<<16), size, kept, index
	return nil
}

// ResetDeadSince clears the backoff of candidates that failed at or after
// min: when a shutdown lifts, their failures said nothing about them.
func (p *Pool) ResetDeadSince(min uint32) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for i := range p.entries {
		if e := &p.entries[i]; e.State == StateDead && e.LastTest >= min {
			e.Fails, e.LastTest = 0, 0
			n++
		}
	}
	return n
}

// ClearFlag drops a flag from every candidate (a list about to be read
// again sets it on its current members).
func (p *Pool) ClearFlag(f uint8) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.entries {
		p.entries[i].Flags &^= f
	}
}

// Minutes converts a time to the pool's compact timestamp.
func Minutes(t time.Time) uint32 { return uint32(t.Unix() / 60) }

// Time converts a compact timestamp back.
func Time(m uint32) time.Time { return time.Unix(int64(m)*60, 0) }
