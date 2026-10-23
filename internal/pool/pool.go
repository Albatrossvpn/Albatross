// Package pool holds every candidate server found in the sources. Links are
// kept in an append-only file on disk and only a compact record per server
// stays in memory, so hundreds of thousands of candidates cost a few tens
// of megabytes.
package pool

import (
	"bufio"
	"io"
	"os"
	"sync"
	"time"
)

// State of a candidate.
const (
	StateNew      uint8 = iota // never tested
	StateDead                  // unreachable (TCP probe or real test failed)
	StateOK                    // worked at the last test
	StateRejected              // the core refused its config
)

// Entry is the in-memory record of one candidate. Keep it small.
type Entry struct {
	Key      uint64
	off      int64
	size     uint32
	Seen     uint32 // refresh cycle that last saw it in a source
	LastTest uint32 // unix minutes
	Source   uint16 // index of the best-scoring source that listed it
	Proto    uint8
	Engine   uint8
	State    uint8
	Fails    uint8
	Pop      uint8 // number of sources listing it, saturating
	Hint     [2]byte
	Flags    uint8
}

// Entry flags.
const (
	FlagUDP   uint8 = 1 << iota // a TCP probe says nothing about it
	FlagFresh                   // listed by a tested or Telegram source
	FlagPlain                   // no transport encryption
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
}

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
		if cur.Seen != p.cycle {
			cur.Seen = p.cycle
			cur.Pop = 1
		} else if cur.Pop < 255 {
			cur.Pop++
		}
		if sourceScore != nil && sourceScore(e.Source) > sourceScore(cur.Source) {
			cur.Source = e.Source
		}
		cur.Flags |= e.Flags & FlagFresh
		if cur.Hint[0] == 0 {
			cur.Hint = e.Hint
		}
		return false
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
	i, ok := p.index[key]
	if !ok {
		p.mu.Unlock()
		return "", false
	}
	e := p.entries[i]
	p.w.Flush()
	p.mu.Unlock()
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

// Compact drops candidates that no source has listed for keepCycles
// cycles (unless they are known to work) and rewrites the file.
func (p *Pool) Compact(keepCycles uint32) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	stale := 0
	for i := range p.entries {
		e := &p.entries[i]
		if e.State != StateOK && p.cycle-e.Seen >= keepCycles {
			stale++
		}
	}
	if stale*3 < len(p.entries) {
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
		if e.State != StateOK && p.cycle-e.Seen >= keepCycles {
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
		return err
	}
	p.f.Close()
	tmp.Close()
	if err := os.Rename(tmpPath, p.path); err != nil {
		return err
	}
	f, err := os.OpenFile(p.path, os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	f.Seek(0, io.SeekEnd)
	p.f, p.w, p.size, p.entries, p.index = f, bufio.NewWriterSize(f, 1<<16), size, kept, index
	return nil
}

// Minutes converts a time to the pool's compact timestamp.
func Minutes(t time.Time) uint32 { return uint32(t.Unix() / 60) }

// Time converts a compact timestamp back.
func Time(m uint32) time.Time { return time.Unix(int64(m)*60, 0) }
