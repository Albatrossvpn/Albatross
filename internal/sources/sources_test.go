package sources

import (
	"strings"
	"testing"
)

// The own-servers list opens only with the key it was sealed with, and is
// offered only when the app was built with one.
func TestSealedOwnList(t *testing.T) {
	defer func(k string) { OwnKey = k }(OwnKey)
	OwnKey = ""
	for _, s := range Central() {
		if s.Sealed {
			t.Fatal("own list offered without a key")
		}
	}
	OwnKey = strings.Repeat("ab", 32)
	if c := Central(); !c[0].Sealed || !strings.HasSuffix(c[0].URL, "/own.bin") {
		t.Fatalf("own list missing: %+v", c[0])
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
	own := Mirrors(CentralBase + "fresh.txt")
	if last := own[len(own)-1]; last != "https://api.github.com/repos/cezomagoanlace29861-jpg/vp/contents/fresh.txt?ref=servers" {
		t.Fatalf("own repo: %v", own)
	}
	for _, m := range Mirrors("https://raw.githubusercontent.com/someone/lists/main/sub.txt") {
		if strings.Contains(m, "api.github.com") {
			t.Fatalf("other repo uses the API: %s", m)
		}
	}
}
