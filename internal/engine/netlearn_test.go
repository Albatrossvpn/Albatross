package engine

import "testing"

// A kind of server that keeps passing moves ahead of one that keeps
// failing, and a handful of tests does not swing the order much.
func TestNetLearnPrefersWhatPasses(t *testing.T) {
	var l netLearn
	const reality, wsTLS = 2*8 + 0 + 1, 1*8 + 1 + 1
	for range 3 {
		l.record(2, reality, true)
	}
	if b := l.snapshot().bonus(2, reality); b > 40 {
		t.Fatalf("three tests gave a bonus of %.0f", b)
	}
	for i := range 400 {
		l.record(2, reality, i%5 == 0)
		l.record(3, wsTLS, i%100 == 0)
	}
	v := l.snapshot()
	if v.bonus(2, reality) <= 20 || v.bonus(3, wsTLS) >= -20 {
		t.Fatalf("bonuses reality %.0f, ws+tls %.0f", v.bonus(2, reality), v.bonus(3, wsTLS))
	}
	dir := t.TempDir()
	l.save(dir)
	var back netLearn
	back.load(dir)
	if back.snapshot().bonus(2, reality) != v.bonus(2, reality) {
		t.Fatal("learning not kept across restarts")
	}
}

// Each network keeps its own lessons; a new one starts from a weaker copy
// of the last, and going back finds the old lessons intact.
func TestNetLearnPerNetwork(t *testing.T) {
	var l netLearn
	const reality, wsTLS = 2*8 + 0 + 1, 1*8 + 1 + 1
	l.use("10.0.0.1") // operator A: Reality passes
	for i := range 400 {
		l.record(2, reality, i%4 == 0)
		l.record(3, wsTLS, false)
	}
	onA := l.snapshot().bonus(2, reality)
	l.use("192.168.1.1") // home Wi-Fi: WebSocket passes
	if b := l.snapshot().bonus(2, reality); b <= 0 || b > onA {
		t.Fatalf("new network started from %.0f (operator A: %.0f)", b, onA)
	}
	for i := range 600 {
		l.record(3, wsTLS, i%4 == 0)
		l.record(2, reality, false)
	}
	if l.snapshot().bonus(3, wsTLS) <= 20 {
		t.Fatal("home network did not learn its own lesson")
	}
	l.use("10.0.0.1")
	if b := l.snapshot().bonus(2, reality); b != onA {
		t.Fatalf("operator A's lesson changed: %.0f, was %.0f", b, onA)
	}
}
