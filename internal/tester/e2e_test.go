package tester

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/cezomagoanlace29861-jpg/vp/internal/core"
	"github.com/cezomagoanlace29861-jpg/vp/internal/node"
)

// TestBatchEndToEnd runs real servers locally (sing-box and Xray inbounds
// that reach the internet through an upstream HTTP proxy) and checks that
// Batch finds the working ones, rejects nothing valid and fails the dead.
//
//	SING_BOX=... XRAY=... E2E_UPSTREAM=http://127.0.0.1:3128 E2E_HOST_IP=192.0.2.2 go test -run EndToEnd ./internal/tester
func TestBatchEndToEnd(t *testing.T) {
	sb, xr, up, hostIP := os.Getenv("SING_BOX"), os.Getenv("XRAY"), os.Getenv("E2E_UPSTREAM"), os.Getenv("E2E_HOST_IP")
	if sb == "" || xr == "" || up == "" || hostIP == "" {
		t.Skip("SING_BOX, XRAY, E2E_UPSTREAM and E2E_HOST_IP must be set")
	}
	// E2E_UPSTREAM=direct lets the test servers reach the internet directly.
	upstream := map[string]any{"type": "direct"}
	xrayUpstream := map[string]any{"protocol": "freedom"}
	if up != "direct" {
		upURL, _ := url.Parse(up)
		upHost, upPortS, _ := net.SplitHostPort(upURL.Host)
		upPort, _ := strconv.Atoi(upPortS)
		upstream = map[string]any{"type": "http", "server": upHost, "server_port": upPort}
		xrayUpstream = map[string]any{"protocol": "http", "settings": map[string]any{"servers": []any{map[string]any{"address": upHost, "port": upPort}}}}
	}
	ports, _ := core.FreePorts(5)
	dir := t.TempDir()
	uuid := "b831381d-6324-4d53-ad4f-8cda48b30811"

	serverSB := map[string]any{
		"log": map[string]any{"level": "warn"},
		"inbounds": []any{
			map[string]any{"type": "vless", "listen": "0.0.0.0", "listen_port": ports[0], "users": []any{map[string]any{"uuid": uuid}}},
			map[string]any{"type": "shadowsocks", "listen": "0.0.0.0", "listen_port": ports[1], "method": "aes-128-gcm", "password": "secret"},
			map[string]any{"type": "vmess", "listen": "0.0.0.0", "listen_port": ports[2], "users": []any{map[string]any{"uuid": uuid}},
				"transport": map[string]any{"type": "ws", "path": "/ws"}},
		},
		"outbounds": []any{upstream},
	}
	serverXray := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{
			"listen": "0.0.0.0", "port": ports[3], "protocol": "vless",
			"settings":       map[string]any{"clients": []any{map[string]any{"id": uuid}}, "decryption": "none"},
			"streamSettings": map[string]any{"network": "xhttp", "xhttpSettings": map[string]any{"path": "/x", "mode": "auto"}},
		}},
		"outbounds": []any{xrayUpstream},
	}
	startServer(t, sb, dir, "server-sb.json", serverSB, "run", "-c")
	startServer(t, xr, dir, "server-xray.json", serverXray, "run", "-c")
	time.Sleep(700 * time.Millisecond)

	links := map[string]bool{
		"vless://" + uuid + "@" + hostIP + ":" + strconv.Itoa(ports[0]) + "?type=tcp&security=none#vless":                                    true,
		"ss://YWVzLTEyOC1nY206c2VjcmV0@" + hostIP + ":" + strconv.Itoa(ports[1]) + "#ss":                                                     true,
		"vless://" + uuid + "@" + hostIP + ":" + strconv.Itoa(ports[3]) + "?type=xhttp&path=%2Fx&mode=auto&security=none&encryption=none#xh": true,
		"vless://" + uuid + "@" + hostIP + ":" + strconv.Itoa(ports[4]) + "?type=tcp&security=none#dead":                                     false,
	}
	vm, _ := json.Marshal(map[string]any{"add": hostIP, "port": ports[2], "id": uuid, "aid": 0, "net": "ws", "path": "/ws", "ps": "vmess"})
	links["vmess://"+b64(vm)] = true

	var nodes []*node.Node
	want := map[*node.Node]bool{}
	for l, ok := range links {
		n, err := node.ParseLink(l)
		if err != nil {
			t.Fatalf("parse %s: %v", l, err)
		}
		nodes = append(nodes, n)
		want[n] = ok
	}
	tst := &Tester{Runtime: &core.Runtime{Bin: core.Binaries{SingBox: sb, Xray: xr}, Dir: dir}, Timeout: 12 * time.Second}

	probes := tst.TCPProbe(context.Background(), nodes)
	for i, n := range nodes {
		if (probes[i] > 0) != want[n] {
			t.Errorf("TCP probe %s = %v", n.Name, probes[i])
		}
	}

	var mu sync.Mutex
	got := map[*node.Node]Result{}
	err := tst.Batch(context.Background(), "e2e", nodes, func(r Result) {
		mu.Lock()
		got[r.Node] = r
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	for n, ok := range want {
		r, seen := got[n]
		if !seen {
			t.Errorf("%s: no result", n.Name)
			continue
		}
		if r.OK != ok {
			t.Errorf("%s: OK=%v err=%s", n.Name, r.OK, r.Err)
			continue
		}
		t.Logf("%-6s ok=%v delay=%v ip=%s country=%s err=%s", n.Name, r.OK, r.Delay.Round(time.Millisecond), r.ExitIP, r.Country, r.Err)
	}
}

func startServer(t *testing.T, bin, dir, name string, cfg map[string]any, args ...string) {
	b, _ := json.Marshal(cfg)
	path := filepath.Join(dir, name)
	os.WriteFile(path, b, 0o600)
	cmd := exec.Command(bin, append(args, path)...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
}

func b64(b []byte) string {
	const enc = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var out []byte
	for i := 0; i < len(b); i += 3 {
		var n uint32
		k := 0
		for j := 0; j < 3; j++ {
			n <<= 8
			if i+j < len(b) {
				n |= uint32(b[i+j])
				k++
			}
		}
		for j := 0; j < 4; j++ {
			if j <= k {
				out = append(out, enc[(n>>(18-6*j))&63])
			} else {
				out = append(out, '=')
			}
		}
	}
	return string(out)
}
