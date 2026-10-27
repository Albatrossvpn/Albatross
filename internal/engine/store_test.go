package engine

import "testing"

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
