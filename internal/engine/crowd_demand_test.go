package engine

import (
	"testing"

	"github.com/Albatrossvpn/vp/internal/sources"
)

// The crowd report carries what the user asked for and could not get
// (demand, by protocol name) and the named diagnostics, as copies that
// counting on does not change, and sending takes them off the pending
// counts. Automatic asks for no protocol.
func TestCrowdReportCarriesDemandAndDiag(t *testing.T) {
	e, err := New(Options{DataDir: t.TempDir(), Sources: []sources.Source{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.pool.Close() })
	e.noteDemand("tuic")
	e.noteDemand("tuic")
	e.noteDemand("")
	e.noteDiag("connect_ok_lt3s")
	e.noteDiag("connect_no_server_chosen")

	e.net.mu.Lock()
	pend := &e.net.table().Pend
	body := pend.report(42)
	e.net.mu.Unlock()
	if body == nil {
		t.Fatal("nothing to report")
	}
	demand, _ := body["demand"].(map[string]uint32)
	diag, _ := body["diag"].(map[string]uint32)
	if len(demand) != 1 || demand["tuic"] != 2 {
		t.Fatalf("demand %v, want tuic: 2 alone", demand)
	}
	if diag["connect_ok_lt3s"] != 1 || diag["connect_no_server_chosen"] != 1 {
		t.Fatalf("diag %v", diag)
	}
	// A copy: a count made while the report is on its way is not in it.
	e.noteDemand("tuic")
	if demand["tuic"] != 2 {
		t.Fatal("the report shares the live map")
	}
	e.net.mu.Lock()
	pend.minus(body)
	left, gone := pend.Demand["tuic"], len(pend.Diag)
	e.net.mu.Unlock()
	if left != 1 || gone != 0 {
		t.Fatalf("after sending: demand for tuic %d (want 1, the one counted meanwhile), diag entries %d (want 0)", left, gone)
	}
	if ttcBucket(2e9) != "lt3s" || ttcBucket(5e9) != "3_10s" || ttcBucket(20e9) != "10_30s" || ttcBucket(60e9) != "gt30s" {
		t.Fatal("ttcBucket")
	}
}
