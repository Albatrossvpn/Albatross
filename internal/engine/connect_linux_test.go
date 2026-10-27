package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Albatrossvpn/vp/internal/node"
	"github.com/Albatrossvpn/vp/internal/sources"
)

// A group whose every server the core refuses is the servers' failure, not
// the local setup's: the connect loop moves on to the next group instead of
// trying the same one again every few seconds, and the refused servers are
// not tried again soon.
func TestRefusedGroupIsTheServersFailure(t *testing.T) {
	bin := t.TempDir()
	// A sing-box that refuses its first outbound, as it does a bad config.
	script := "#!/bin/sh\necho 'FATAL[0000] start service: initialize outbound[0]: bad config' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "sing-box"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	e, err := New(Options{DataDir: t.TempDir(), AppDirs: []string{bin}, Sources: []sources.Source{}, Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.pool.Close()
	var cancel context.CancelFunc
	e.ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	link := "trojan://pw@192.0.2.1:443?sni=a.example.com#t"
	n, err := node.ParseLink(link)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Key: n.Key(), key64: node.Key64(n.Key()), Link: link, Proto: n.Proto, Country: "DE"}
	e.good[s.key64] = s
	err = e.start(e.connGen, "", []*Server{s}, false)
	if !errors.Is(err, errServers) {
		t.Fatalf("start = %v, want the servers' failure", err)
	}
	if s.Fails != 3 {
		t.Fatalf("the refused server has %d fails, want 3", s.Fails)
	}
}
