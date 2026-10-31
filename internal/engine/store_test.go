package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Albatrossvpn/vp/internal/sources"
)

func TestProtocolChoiceMatches(t *testing.T) {
	warp := &Server{Proto: "warpplus", Source: "warp-plus"}
	tor := &Server{Proto: "warpplus", Source: "tor"}
	naive := &Server{Proto: "naive"}
	if !warp.matches("warp") || warp.matches("tor") || !tor.matches("tor") || tor.matches("warp") || !naive.matches("naive") || naive.matches("warp") || !naive.matches("") {
		t.Fatal("protocol choices do not pick the right servers")
	}
	for _, c := range ProtocolChoices() {
		if c.Value != "" && c.Label == "" {
			t.Fatalf("choice %q has no label", c.Value)
		}
	}
}

// Every protocol on offer has a number in the pool: a choice without one
// would test every protocol and find its own servers only by chance.
func TestProtocolChoicesAreScannedFor(t *testing.T) {
	for _, c := range ProtocolChoices() {
		switch c.Value {
		case "", "warp", "tor": // automatic, and the methods that are not servers
			continue
		case "dns", "icmp": // the carriers: counted from the good servers, not the pool
			continue
		}
		if protoCodes[c.Value] == 0 {
			t.Errorf("%s (%s) has no protocol code", c.Value, c.Label)
		}
	}
	seen := map[uint8]string{}
	for name, code := range protoCodes {
		if other, dup := seen[code]; dup {
			t.Errorf("%s and %s share the code %d", name, other, code)
		}
		seen[code] = name
	}
}

// Forgetting a known server removes it and logs it without locking the
// engine up (the log takes the engine's lock too).
func TestForgetDoesNotDeadlock(t *testing.T) {
	e, err := New(Options{DataDir: t.TempDir(), Sources: []sources.Source{}, Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.pool.Close()
	e.good[7] = &Server{Name: "gone", key64: 7}
	done := make(chan struct{})
	go func() {
		e.forget(7)
		e.forget(8) // unknown: nothing to do
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("forget locked the engine up")
	}
	if _, ok := e.good[7]; ok {
		t.Fatal("the server is still known")
	}
	if logs := e.Logs(); len(logs) == 0 || !strings.Contains(logs[len(logs)-1].Text, "gone") {
		t.Fatalf("logs = %+v", logs)
	}
}

// The first start of a version that judges servers anew forgets the
// servers and test results kept from before, once, and keeps the copies of
// the lists while asking for them again.
func TestFreshStartForgetsOldResults(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "cache"), 0o755)
	for _, f := range []string{"servers.json", "states.bin", "settings.json", "cache/sub-x.gz", "cache/sub-x.json"} {
		os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644)
	}
	freshStart(dir)
	for _, f := range []string{"servers.json", "states.bin", "cache/sub-x.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Fatalf("%s kept", f)
		}
	}
	for _, f := range []string{"settings.json", "cache/sub-x.gz"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("%s gone", f)
		}
	}
	os.WriteFile(filepath.Join(dir, "servers.json"), []byte("x"), 0o644)
	freshStart(dir)
	if _, err := os.Stat(filepath.Join(dir, "servers.json")); err != nil {
		t.Fatal("forgot again on the next start")
	}
}
