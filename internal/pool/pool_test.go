package pool

import (
	"path/filepath"
	"strconv"
	"testing"
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
	for c := 0; c < 6; c++ {
		p.NextCycle()
	}
	p.Add(Entry{Key: 9}, "link-8", nil)
	if err := p.Compact(6); err != nil {
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
