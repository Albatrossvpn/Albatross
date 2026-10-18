package engine

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cezomagoanlace29861-jpg/vp/internal/core"
	"github.com/cezomagoanlace29861-jpg/vp/internal/sources"
	"github.com/cezomagoanlace29861-jpg/vp/internal/tester"
)

// TestEngineEndToEnd runs the whole engine against local servers: it
// fetches a subscription, tests every server, connects, proves traffic
// flows through the VPN and disconnects.
//
//	SING_BOX=... XRAY=... E2E_UPSTREAM=http://127.0.0.1:3128 E2E_HOST_IP=192.0.2.2 go test -run EngineEndToEnd ./internal/engine
func TestEngineEndToEnd(t *testing.T) {
	sb, xr, up, hostIP := os.Getenv("SING_BOX"), os.Getenv("XRAY"), os.Getenv("E2E_UPSTREAM"), os.Getenv("E2E_HOST_IP")
	if sb == "" || xr == "" || up == "" || hostIP == "" {
		t.Skip("SING_BOX, XRAY, E2E_UPSTREAM and E2E_HOST_IP must be set")
	}
	// E2E_UPSTREAM=direct lets the test servers reach the internet directly.
	upstream := map[string]any{"type": "direct"}
	if up != "direct" {
		upURL, _ := url.Parse(up)
		upHost, upPortS, _ := net.SplitHostPort(upURL.Host)
		upPort, _ := strconv.Atoi(upPortS)
		upstream = map[string]any{"type": "http", "server": upHost, "server_port": upPort}
	}
	ports, _ := core.FreePorts(4)
	dir := t.TempDir()
	uuid := "b831381d-6324-4d53-ad4f-8cda48b30811"
	server := map[string]any{
		"log": map[string]any{"level": "error"},
		"inbounds": []any{
			map[string]any{"type": "vless", "listen": "0.0.0.0", "listen_port": ports[0], "users": []any{map[string]any{"uuid": uuid}}},
			map[string]any{"type": "trojan", "listen": "0.0.0.0", "listen_port": ports[1], "users": []any{map[string]any{"password": "pw"}},
				"transport": map[string]any{"type": "ws", "path": "/t"}},
		},
		"outbounds": []any{upstream},
	}
	b, _ := json.Marshal(server)
	cfgPath := filepath.Join(dir, "server.json")
	os.WriteFile(cfgPath, b, 0o600)
	cmd := exec.Command(sb, "run", "-c", cfgPath)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	for _, p := range ports[:2] {
		for i := 0; i < 50; i++ {
			if c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(p)); err == nil {
				c.Close()
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	sub := strings.Join([]string{
		fmt.Sprintf("vless://%s@%s:%d?type=tcp&security=none#%%F0%%9F%%87%%A9%%F0%%9F%%87%%AA vless", uuid, hostIP, ports[0]),
		fmt.Sprintf("trojan://pw@%s:%d?type=ws&path=%%2Ft&security=none#trojan-ws", hostIP, ports[1]),
		fmt.Sprintf("vless://%s@%s:%d?type=tcp&security=none#dead", uuid, hostIP, ports[2]),
		"garbage line",
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(sub)) }))
	defer srv.Close()

	e, err := New(Options{
		DataDir: filepath.Join(dir, "data"),
		Sources: []sources.Source{{ID: "local", Kind: sources.KindSubscription, URL: srv.URL, Tier: sources.TierPriority, Builtin: true, Enabled: true}},
		Version: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	e.bin = core.Binaries{SingBox: sb, Xray: xr}
	e.rt.Bin = e.bin
	e.rt.OnLine = func(c, l string) { t.Logf("[%s] %s", c, l) }
	s := e.Settings()
	s.ProxyPort = ports[3]
	s.Warp = false
	e.settings = s
	e.Start()
	defer e.Close()

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if len(e.Servers("", 0)) >= 2 {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	servers := e.Servers("", 0)
	if len(servers) != 2 {
		t.Fatalf("want 2 working servers, got %d (%v)", len(servers), e.Logs())
	}
	for _, s := range servers {
		t.Logf("working: %s %s country=%s delay=%dms", s.Proto, s.Name, s.Country, s.Delay)
	}
	country := servers[0].Country
	if err := e.Connect(country); err != nil {
		t.Fatalf("connect: %v", err)
	}
	snap := e.Snapshot()
	if snap.Status != StatusConnected || snap.Conn == nil {
		t.Fatalf("status %s: %s", snap.Status, snap.Message)
	}
	t.Logf("connected: %s via %s, exit %s, %d ms, group %d", snap.Conn.CountryName, snap.Conn.Server, snap.Conn.ExitIP, snap.Conn.Delay, snap.Conn.Group)
	r := tester.Probe(t.Context(), &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(ports[3])}, 10*time.Second)
	if !r.OK {
		t.Fatalf("traffic through the VPN failed: %s", r.Err)
	}
	if len(snap.Countries) == 0 || snap.Countries[0].Servers != 2 {
		t.Fatalf("countries = %+v", snap.Countries)
	}
	e.Disconnect()
	if e.Snapshot().Conn != nil {
		t.Fatal("still connected")
	}
	if c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(ports[3]), time.Second); err == nil {
		c.Close()
		t.Fatal("proxy port still open after disconnect")
	}
}
