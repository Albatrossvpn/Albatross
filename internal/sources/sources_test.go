package sources

import (
	"slices"
	"strings"
	"testing"
)

// The own-servers list opens only with the key it was sealed with, and is
// offered only when the app was built with one.
func TestSealedOwnList(t *testing.T) {
	defer func(k string) { OwnKey = k }(OwnKey)
	OwnKey = ""
	for _, s := range Central(7) {
		if s.Sealed {
			t.Fatal("own list offered without a key")
		}
	}
	OwnKey = strings.Repeat("ab", 32)
	if c := Central(7); !c[0].Sealed || !c[1].Sealed || c[0].URL == c[1].URL || !strings.Contains(c[0].URL, "/own-") {
		t.Fatalf("own lists missing: %+v", c[:2])
	}
	// Every install reads two different parts, and every part is read.
	seen := map[string]bool{}
	for i := range uint32(64) {
		c := Central(i)
		if c[0].URL == c[1].URL {
			t.Fatalf("install %d reads one part twice", i)
		}
		seen[c[0].URL], seen[c[1].URL] = true, true
	}
	if len(seen) != OwnBuckets {
		t.Fatalf("%d parts read, want %d", len(seen), OwnBuckets)
	}
	sealed, err := Seal(ownKey(), "vless://a\ntrojan://b\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := Open(string(sealed)); got != "vless://a\ntrojan://b\n" {
		t.Fatalf("opened %q", got)
	}
	OwnKey = strings.Repeat("cd", 32)
	if Open(string(sealed)) != "" {
		t.Fatal("opened with the wrong key")
	}
	if strings.Contains(string(sealed), "vless") {
		t.Fatal("sealed list readable")
	}
}

func TestMirrorsUseGitHubAPIOnlyForOwnRepo(t *testing.T) {
	defer func(old string) { WorkerMirror = old }(WorkerMirror)
	WorkerMirror = "https://lists.example.workers.dev"
	own := Mirrors(CentralBase + "fresh.txt")
	if !slices.Contains(own, "https://api.github.com/repos/cezomagoanlace29861-jpg/Albatross/contents/fresh.txt?ref=servers") ||
		!slices.Contains(own, WorkerMirror+"/list/servers/fresh.txt") {
		t.Fatalf("own repo: %v", own)
	}
	for _, m := range Mirrors("https://raw.githubusercontent.com/someone/lists/main/sub.txt") {
		if strings.Contains(m, "api.github.com") || strings.HasPrefix(m, WorkerMirror) {
			t.Fatalf("other repo uses the API: %s", m)
		}
	}
}
