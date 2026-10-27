package core

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Albatrossvpn/vp/internal/node"
)

// TestFakeSidecarHelper is the helper program of TestSidecarRunsInItsOwnDir:
// it listens where its --socks5 argument says and waits to be stopped.
func TestFakeSidecarHelper(t *testing.T) {
	if os.Getenv("ALBATROSS_FAKE_SIDECAR") == "" {
		t.Skip("only as the helper of another test")
	}
	args := os.Args
	for i, a := range args {
		if a == "--socks5" && i+1 < len(args) {
			l, err := net.Listen("tcp", args[i+1])
			if err != nil {
				os.Exit(2)
			}
			defer l.Close()
			time.Sleep(time.Minute)
			os.Exit(0)
		}
	}
	os.Exit(3)
}

// A sidecar helper runs with its files in a directory of its own run, which
// goes when the helper ends.
func TestSidecarRunsInItsOwnDir(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "brook")
	script := "#!/bin/sh\nexec " + strconv.Quote(os.Args[0]) + " -test.run=^TestFakeSidecarHelper$ -- \"$@\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ALBATROSS_FAKE_SIDECAR", "1")
	n, err := node.ParseLink("brook://server?server=192.0.2.1%3A9999&password=hello")
	if err != nil {
		t.Fatal(err)
	}
	r := &Runtime{Bin: Binaries{Sidecars: map[string]string{"brook": bin}}, Dir: t.TempDir()}
	ports, err := FreePorts(2)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a := r.startSidecar(ctx, n, ports[0])
	b := r.startSidecar(ctx, n, ports[1])
	if a == nil || b == nil {
		t.Fatal("the helper did not start")
	}
	dirs, _ := filepath.Glob(filepath.Join(r.Dir, "sidecar", "brook-*"))
	if len(dirs) != 2 {
		t.Fatalf("one node run twice at once has %d directories, want 2", len(dirs))
	}
	a.Stop()
	<-a.Done()
	deadline := time.Now().Add(5 * time.Second)
	for {
		dirs, _ = filepath.Glob(filepath.Join(r.Dir, "sidecar", "brook-*"))
		if len(dirs) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after one helper ended: %v", dirs)
		}
		time.Sleep(50 * time.Millisecond)
	}
	b.Stop()
}
