package engine

import (
	"testing"
	"time"

	"github.com/Albatrossvpn/vp/internal/node"
)

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

func TestLearnPortAndLife(t *testing.T) {
	var l netLearn
	l.use("net")
	n443 := &node.Node{Proto: "vless", Server: "a.example", Port: 443, Security: "tls", Transport: "ws"}
	n2053 := *n443
	n2053.Port = 2053
	for range 300 {
		l.record(1, n443.Style(), false)
		l.record(1, n2053.Style(), true)
	}
	v := l.snapshot()
	if v.bonus(1, n2053.Style()) <= v.bonus(1, n443.Style()) {
		t.Fatal("port 2053 should rank above 443 here")
	}
	if _, ok := l.life(n443.Style()); ok {
		t.Fatal("no lifetime known yet")
	}
	for _, d := range []time.Duration{10 * time.Second, 20 * time.Minute, 24 * time.Minute, 22 * time.Minute} {
		l.recordLife(n443.Style(), d)
	}
	if d, ok := l.life(n443.Style()); !ok || d < 20*time.Minute || d > 24*time.Minute {
		t.Fatalf("life = %v %v", d, ok)
	}
}

func TestBlockedNetworksSink(t *testing.T) {
	var l netLearn
	l.use("op1")
	for i := 0; i < 40; i++ {
		l.record(1, 0, i%10 == 0)
	}
	for i := 0; i < 5; i++ {
		l.recordNet(7, false)
		l.recordNet(9, true)
	}
	v := l.snapshot()
	if v.netBonus(7) >= -20 || v.netBonus(9) <= 20 || v.netBonus(8) != 0 {
		t.Fatalf("blocked %v, open %v, unknown %v", v.netBonus(7), v.netBonus(9), v.netBonus(8))
	}
	// Another operator starts without this one's blocks.
	l.use("op2")
	if l.snapshot().netBonus(7) != 0 {
		t.Fatal("blocks leaked to another network")
	}
}
