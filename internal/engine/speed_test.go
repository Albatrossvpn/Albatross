//go:build unix

package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cezomagoanlace29861-jpg/vp/internal/core"
	"github.com/cezomagoanlace29861-jpg/vp/internal/sources"
)

// TestSpeedRoundMovesOffSlowServer runs a real core in front of three local
// servers that answer pings equally fast but carry very different amounts:
// the one in use is throttled. A speed round must move to a fast one, leave
// a fast member alone, and not swap again right after a swap. Everything is
// local, so it needs only sing-box.
//
//	SING_BOX=... go test -run SpeedRound ./internal/engine
func TestSpeedRoundMovesOffSlowServer(t *testing.T) {
	sb := os.Getenv("SING_BOX")
	if sb == "" {
		t.Skip("SING_BOX must be set")
	}
	content := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("bytes"))
		w.Header().Set("Content-Length", strconv.Itoa(n))
		buf := make([]byte, 32<<10)
		for n > 0 {
			k := min(n, len(buf))
			if _, err := w.Write(buf[:k]); err != nil {
				return
			}
			n -= k
		}
	}))
	defer content.Close()
	old := speedURL
	speedURL = content.URL + "/__down?bytes="
	defer func() { speedURL = old }()

	ports, _ := core.FreePorts(8)
	dir := t.TempDir()
	uuid := "b831381d-6324-4d53-ad4f-8cda48b30811"
	run := func(name string, cfg map[string]any) {
		b, _ := json.Marshal(cfg)
		path := filepath.Join(dir, name+".json")
		os.WriteFile(path, b, 0o600)
		cmd := exec.Command(sb, "run", "-c", path)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	}
	// Each server reaches the content through its own throttle.
	limits := make([]*atomic.Int64, 3)
	var outbounds []any
	var tags []string
	for i := range 3 {
		limits[i] = &atomic.Int64{}
		tp := throttleProxy(t, limits[i])
		run(fmt.Sprint("server", i), map[string]any{
			"log":       map[string]any{"level": "error"},
			"inbounds":  []any{map[string]any{"type": "vless", "listen": "127.0.0.1", "listen_port": ports[i], "users": []any{map[string]any{"uuid": uuid}}}},
			"outbounds": []any{map[string]any{"type": "http", "server": "127.0.0.1", "server_port": tp}},
		})
		waitPort(t, ports[i])
		tag := fmt.Sprint("m", i)
		tags = append(tags, tag)
		outbounds = append(outbounds, map[string]any{"type": "vless", "tag": tag, "server": "127.0.0.1", "server_port": ports[i], "uuid": uuid})
	}
	c := &connection{
		home: "DE", clashAddr: "127.0.0.1:" + strconv.Itoa(ports[4]), clashSecret: "secret",
		probeAddr: "127.0.0.1:" + strconv.Itoa(ports[5]), lastBusy: time.Now(), wake: make(chan struct{}, 1),
	}
	cfg := connectConfig(&core.Set{Tags: tags, Outbounds: outbounds}, connOptions{
		listen: "127.0.0.1", port: ports[3], clashAddr: c.clashAddr, clashSecret: c.clashSecret, probeAddr: c.probeAddr,
	})
	cfg["log"] = map[string]any{"level": "error"}
	run("client", cfg)
	waitPort(t, ports[4])
	for i, tag := range tags {
		c.members = append(c.members, &member{tag: tag, country: "DE", alive: true, delay: 100 + i, server: &Server{Name: tag}})
	}

	e, err := New(Options{DataDir: t.TempDir(), Sources: []sources.Source{}})
	if err != nil {
		t.Fatal(err)
	}
	defer e.pool.Close()
	ctx := t.Context()
	now := func() string { return selected(t, c) }

	// m0 is in use and carries 100 KB/s; the others are unlimited.
	limits[0].Store(100 << 10)
	if err := c.use(ctx, c.members[0]); err != nil {
		t.Fatal(err)
	}
	c.switched = time.Time{}
	e.speedRound(ctx, c)
	if got := now(); got == "m0" {
		t.Fatalf("still on the slow server (speeds %v)", speeds(c))
	}
	t.Logf("moved to %s; speeds %v", now(), speeds(c))
	for _, l := range e.Logs() {
		t.Logf("  %s", l.Text)
	}

	// Right after a swap nothing is measured or swapped, even if the new
	// member turns slow.
	cur := now()
	for i, tag := range tags {
		if tag == cur {
			limits[i].Store(50 << 10)
		}
	}
	c.speedAt = time.Time{}
	e.speedRound(ctx, c)
	if now() != cur {
		t.Fatal("swapped again right after a swap")
	}

	// A fast member is left alone and its backups are not tested.
	for _, l := range limits {
		l.Store(0)
	}
	c.mu.Lock()
	c.switched, c.speedAt = time.Now().Add(-time.Hour), time.Time{}
	for _, m := range c.members {
		m.speedAt = time.Time{}
	}
	c.mu.Unlock()
	e.speedRound(ctx, c)
	if now() != cur {
		t.Fatal("left a fast server")
	}
	for _, m := range c.members {
		if m.tag != cur && !m.speedAt.IsZero() {
			t.Fatalf("tested backup %s although the server in use is fast", m.tag)
		}
	}
}

// selected asks the core which member the group uses.
func selected(t *testing.T, c *connection) string {
	t.Helper()
	var r struct {
		Now string `json:"now"`
	}
	if _, err := c.api(context.Background(), http.MethodGet, "/proxies/proxy", nil, &r, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	return r.Now
}

func speeds(c *connection) map[string]string {
	out := map[string]string{}
	for _, m := range c.members {
		out[m.tag] = rate(m.speed)
	}
	return out
}

// throttleProxy is an HTTP CONNECT proxy that copies at most limit bytes a
// second towards the client (0 = no limit).
func throttleProxy(t *testing.T, limit *atomic.Int64) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up, err := net.Dial("tcp", r.Host)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer up.Close()
		w.WriteHeader(http.StatusOK)
		down, buf, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer down.Close()
		go io.Copy(up, buf)
		chunk := make([]byte, 8<<10)
		for {
			n, err := up.Read(chunk)
			if n > 0 {
				if _, werr := down.Write(chunk[:n]); werr != nil {
					return
				}
				if lim := limit.Load(); lim > 0 {
					time.Sleep(time.Duration(float64(n) / float64(lim) * float64(time.Second)))
				}
			}
			if err != nil {
				return
			}
		}
	})}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return l.Addr().(*net.TCPAddr).Port
}
