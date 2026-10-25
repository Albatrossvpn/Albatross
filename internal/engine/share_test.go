package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/Albatrossvpn/vp/internal/sources"
)

func TestShareCode(t *testing.T) {
	a, err := New(Options{DataDir: t.TempDir(), Sources: []sources.Source{}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := a.ShareCode(); err == nil {
		t.Fatal("a code with no servers")
	}
	link := "trojan://pw@a.example.com:443?security=tls&sni=a.example.com#one"
	a.mu.Lock()
	a.good[1] = &Server{Key: "k", key64: 1, Link: link, Name: "one", Country: "DE", LastOK: time.Now(), Delay: 100}
	a.mu.Unlock()
	code, err := a.ShareCode()
	if err != nil || !strings.HasPrefix(code, sharePrefix) || strings.Contains(code, "a.example.com") {
		t.Fatalf("code %q, %v", code, err)
	}

	b, err := New(Options{DataDir: t.TempDir(), Sources: []sources.Source{}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	// People paste codes with text around them.
	n, err := b.ImportShareCode("here you go: " + code + " \n")
	if err != nil || n != 1 {
		t.Fatalf("imported %d, %v", n, err)
	}
	if _, err := b.ImportShareCode(code[:len(code)-4] + "AAAA"); err == nil {
		t.Fatal("an edited code was accepted")
	}
}

func TestShareMirrors(t *testing.T) {
	text, mirrors := splitShareMirrors("vless://a\n#mirror http://5.6.7.8:8081/abc\n#mirror https://m.example.ir\n#mirror http://evil.example.com\n#mirror http://10.0.0.1\n#mirror http://5.6.7.8/x/\nss://b")
	if text != "vless://a\nss://b" {
		t.Fatalf("text %q", text)
	}
	if len(mirrors) != 3 || mirrors[0] != "http://5.6.7.8:8081/abc" || mirrors[1] != "https://m.example.ir" || mirrors[2] != "http://5.6.7.8/x" {
		t.Fatalf("mirrors %v", mirrors)
	}
	for _, bad := range []string{"ftp://1.2.3.4", "http://user@1.2.3.4", "http://1.2.3.4/?q=1", "http://127.0.0.1", "http://", "http://1.2.3.4#f"} {
		if validMirror(bad) {
			t.Errorf("%s accepted", bad)
		}
	}
	s := Settings{DomesticMirrors: []string{"http://1.2.3.4/", "http://evil.example.com", "http://1.2.3.4"}}
	s.normalize()
	if len(s.DomesticMirrors) != 2 || s.DomesticMirrors[0] != "http://1.2.3.4" {
		t.Fatalf("normalized %v", s.DomesticMirrors)
	}
}
