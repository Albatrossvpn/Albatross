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
