package engine

import (
	"testing"

	"github.com/cezomagoanlace29861-jpg/vp/internal/node"
)

func TestHelloLearning(t *testing.T) {
	var l netLearn
	l.use("net")
	// On this network Chrome's hello is dropped, Edge's passes, and
	// fragmenting makes things worse.
	for range 200 {
		l.recordHello(helloCode(0, false), false)
		l.recordHello(helloCode(0, true), false)
		l.recordHello(helloCode(1, false), true)
		l.recordHello(helloCode(1, true), false)
	}
	v := l.snapshot()
	never := func() float64 { return 0.99 }
	if got := v.pickHello(false, never); helloFPs[got] != "edge" {
		t.Fatalf("picked %s, want edge", helloFPs[got])
	}
	if !v.fragHurts(1) || v.fragHelps(1) {
		t.Fatal("fragmenting should count as harmful here")
	}
	if got := v.nextHello(1, false); helloFPs[got] == "edge" {
		t.Fatal("nextHello returned the skipped hello")
	}
}

func TestHelloReachesTheCores(t *testing.T) {
	n, err := node.ParseLink("trojan://pw@example.com:443?security=tls&sni=example.com&type=ws&path=%2F#x")
	if err != nil || !helloable(n) {
		t.Fatalf("parse: %v", err)
	}
	c := candidate{node: n}
	c.setHello(helloIndex("safari"))
	if n.HelloFP != "" || c.node.HelloFP != "safari" || c.fp() != 2 {
		t.Fatal("setHello must work on a copy")
	}
	ob, _ := c.node.SingBox("t", node.SingBoxOptions{})
	tls := ob["tls"].(map[string]any)
	if tls["utls"].(map[string]any)["fingerprint"] != "safari" {
		t.Fatalf("utls = %v", tls["utls"])
	}
	if a := tls["alpn"].([]string); len(a) != 1 || a[0] != "http/1.1" {
		t.Fatalf("alpn = %v", a)
	}
}
