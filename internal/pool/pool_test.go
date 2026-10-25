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
