package engine

import (
	"testing"
	"time"

	"github.com/cezomagoanlace29861-jpg/vp/internal/pool"
	"github.com/cezomagoanlace29861-jpg/vp/internal/sources"
)

func TestDistrust(t *testing.T) {
	e, err := New(Options{DataDir: t.TempDir(), Sources: []sources.Source{}})
	if err != nil {
		t.Fatal(err)
	}
	defer e.pool.Close()
	const key = 42
	e.pool.Add(pool.Entry{Key: key}, "vless://x@1.2.3.4:443", nil)
	e.good[key] = &Server{Name: "spy", key64: key}

	// Until some server shows a genuine certificate, a forged one may be
	// this device's doing (a security program, a missing root), so
	// nothing is dropped.
	if e.distrust(key, "spy") {
		t.Fatal("dropped a server without proof the device checks certificates")
	}
	if en, _ := e.pool.Get(key); en.State == pool.StateRejected || e.good[key] == nil {
		t.Fatal("server changed without proof")
	}

	e.mu.Lock()
	e.httpsOK = time.Now()
	e.mu.Unlock()
	if !e.distrust(key, "spy") {
		t.Fatal("forging server kept")
	}
	if en, _ := e.pool.Get(key); en.State != pool.StateRejected {
		t.Fatalf("state %d: the server would be tested again", en.State)
	}
	if e.good[key] != nil {
		t.Fatal("the server is still offered")
	}
}

func TestCountryNamesAreEnglish(t *testing.T) {
	if got := countryName("DE"); got != "Germany" {
		t.Fatalf("countryName(DE) = %q; the interface is English", got)
	}
}
