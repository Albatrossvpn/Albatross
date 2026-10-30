package obf

import "testing"

func TestHideReveal(t *testing.T) {
	for _, s := range []string{"", "https://a.example.com,https://b.example.com", "x", "a mirror with spaces & symbols/?="} {
		enc := Hide(s)
		if s != "" && (enc == s || !hasMark(enc)) {
			t.Fatalf("not hidden: %q -> %q", s, enc)
		}
		if got := Reveal(enc); got != s {
			t.Fatalf("round trip: %q -> %q -> %q", s, enc, got)
		}
	}
	// A plain value (no mark) is left as it is: dev builds inject plaintext.
	if got := Reveal("https://plain.example.com"); got != "https://plain.example.com" {
		t.Fatalf("plain changed: %q", got)
	}
}

func hasMark(s string) bool { return len(s) >= len(mark) && s[:len(mark)] == mark }
