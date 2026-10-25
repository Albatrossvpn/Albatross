package pool

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestAddLinkCompact(t *testing.T) {
	p, err := Open(filepath.Join(t.TempDir(), "pool.dat"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for i := 0; i < 100; i++ {
		if !p.Add(Entry{Key: uint64(i + 1)}, "link-"+strconv.Itoa(i), nil) {
			t.Fatalf("key %d should be new", i)
		}
	}
	if p.Add(Entry{Key: 5}, "dup", nil) {
		t.Fatal("duplicate key added")
	}
	if e, _ := p.Get(5); e.Pop != 2 {
		t.Fatalf("popularity = %d", e.Pop)
	}
	if l, ok := p.Link(42); !ok || l != "link-41" {
		t.Fatalf("Link(42) = %q", l)
	}
	p.Update(7, func(e *Entry) { e.State = StateOK })
	// Three days pass without any source listing them again.
	for i := range p.entries {
		p.entries[i].SeenAt -= 3 * 24 * 60
	}
	p.Add(Entry{Key: 9}, "link-8", nil)
	if err := p.Compact(48*time.Hour, 0); err != nil {
		t.Fatal(err)
	}
	if p.Len() != 2 {
		t.Fatalf("after compact len = %d, want 2 (one working, one recently seen)", p.Len())
	}
	if l, _ := p.Link(7); l != "link-6" {
		t.Fatalf("Link(7) after compact = %q", l)
	}
	if l, _ := p.Link(9); l != "link-8" {
		t.Fatalf("Link(9) after compact = %q", l)
	}
	p.Add(Entry{Key: 1000}, "after-compact", nil)
	if l, _ := p.Link(1000); l != "after-compact" {
		t.Fatalf("append after compact = %q", l)
	}
}

// A rejected server is never dropped by Compact, so a list cannot bring it
// back as a fresh candidate.
func TestCompactKeepsRejected(t *testing.T) {
	p, err := Open(filepath.Join(t.TempDir(), "pool"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for i := 0; i < 20; i++ {
		p.Add(Entry{Key: uint64(i + 1)}, "vless://x@h:1#"+strconv.Itoa(i), nil)
	}
	p.Update(1, func(e *Entry) { e.State = StateRejected })
	p.Update(2, func(e *Entry) { e.State = StateDead })
	// Everyone was last seen long ago.
	for i := range p.entries {
		p.entries[i].SeenAt = 1
	}
	if err := p.Compact(time.Hour, 5); err != nil {
		t.Fatal(err)
	}
	var rejected, dead bool
	p.Each(func(e *Entry) {
		rejected = rejected || e.Key == 1
		dead = dead || e.Key == 2
	})
	if !rejected || dead {
		t.Fatalf("rejected kept: %v, dead kept: %v", rejected, dead)
	}
	if p.Add(Entry{Key: 1}, "vless://x@h:1#0", nil) {
		t.Fatal("a rejected server came back as new")
	}
}

func TestCompactKeepsHistoryOfDropped(t *testing.T) {
	p, err := Open(filepath.Join(t.TempDir(), "pool.dat"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	score := func(uint16) float64 { return 1 }
	// An old dead entry, to be dropped, and a fresh one that stays.
	old := Entry{Key: 1, SeenAt: Minutes(time.Now().Add(-48 * time.Hour)), State: StateDead, Fails: 4, LastTest: Minutes(time.Now().Add(-time.Hour))}
	p.Add(old, "vless://a", score)
	p.Update(1, func(e *Entry) { e.SeenAt = Minutes(time.Now().Add(-48 * time.Hour)) })
	p.Add(Entry{Key: 2}, "vless://b", score)
	if err := p.Compact(24*time.Hour, 0); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Get(1); ok {
		t.Fatal("the old dead entry should have been dropped")
	}
	// A list brings it back: its history comes with it.
	if !p.Add(Entry{Key: 1}, "vless://a", score) {
		t.Fatal("re-adding a dropped key must be new")
	}
	e, _ := p.Get(1)
	if e.State != StateDead || e.Fails != 4 || e.LastTest != old.LastTest {
		t.Fatalf("history lost: %+v", e)
	}
	if l, ok := p.Link(1); !ok || l != "vless://a" {
		t.Fatalf("link %q %v", l, ok)
	}
}
