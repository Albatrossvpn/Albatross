package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/cezomagoanlace29861-jpg/vp/internal/sources"
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
