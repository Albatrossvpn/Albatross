package engine

import "testing"

// The bot reads the account from the link: "rn" and the username in
// base32 (backend renewTarget decodes "rnmfrgg" to "abc").
func TestRenewURL(t *testing.T) {
	if got := renewURL("AlbatrossHelp_bot", "abc"); got != "https://t.me/AlbatrossHelp_bot?start=rnmfrgg" {
		t.Fatal(got)
	}
	long := "a2345678901234567890123456789012"
	if got := renewURL("AlbatrossHelp_bot", long); len(got) > len("https://t.me/AlbatrossHelp_bot?start=")+64 {
		t.Fatalf("a link's start carries 64 characters at most: %s", got)
	}
	if got := renewURL("AlbatrossHelp_bot", long+"x"); got != "https://t.me/AlbatrossHelp_bot?start=buy" {
		t.Fatal(got)
	}
	if botName.MatchString("evil.example/x") || !botName.MatchString("AlbatrossHelp_bot") {
		t.Fatal("bot name check")
	}
}
