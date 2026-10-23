//go:build linux

package mobile

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/sys/unix"
)

// TestAndroidPath runs the Android app's whole data path on Linux. A fake
// platform plays the VpnService: it creates a TUN device and hands its
// descriptor to StartTun, as TunnelService does. The test connects through
// the same web API the page uses, then sends HTTPS and DNS into the tunnel
// and expects them to come out through the VPN server.
//
// It needs root (for the TUN device), the ip tool and the e2e variables:
//
//	SING_BOX=... XRAY=... E2E_UPSTREAM=http://127.0.0.1:3128 E2E_HOST_IP=192.0.2.2 go test -run AndroidPath ./mobile
func TestAndroidPath(t *testing.T) {
	sb, xr, up, hostIP := os.Getenv("SING_BOX"), os.Getenv("XRAY"), os.Getenv("E2E_UPSTREAM"), os.Getenv("E2E_HOST_IP")
	if sb == "" || xr == "" || up == "" || hostIP == "" {
		t.Skip("SING_BOX, XRAY, E2E_UPSTREAM and E2E_HOST_IP must be set")
	}
	if _, err := exec.LookPath("ip"); err != nil || os.Geteuid() != 0 {
		t.Skip("needs root and the ip tool to make a TUN device")
	}
	dir := t.TempDir()
	// The cores under the names the app ships them with.
	lib := filepath.Join(dir, "lib")
	os.MkdirAll(lib, 0o755)
	os.Symlink(sb, filepath.Join(lib, "libsingbox.so"))
	os.Symlink(xr, filepath.Join(lib, "libxray.so"))

	// One VPN server whose exit is the upstream.
	upstream := map[string]any{"type": "direct"}
	if up != "direct" {
		u, _ := url.Parse(up)
		h, p, _ := net.SplitHostPort(u.Host)
		port, _ := strconv.Atoi(p)
		upstream = map[string]any{"type": "http", "server": h, "server_port": port}
	}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	uuid := "b831381d-6324-4d53-ad4f-8cda48b30811"
	// The far side of the VPN: a web server only the VPN server reaches,
	// at a documentation address nothing else routes.
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hello through the VPN") }))
	defer web.Close()
	webPort, _ := strconv.Atoi(web.URL[strings.LastIndex(web.URL, ":")+1:])
	upstream["tag"] = "up"
	b, _ := json.Marshal(map[string]any{
		"log":       map[string]any{"level": "error"},
		"inbounds":  []any{map[string]any{"type": "vless", "listen": "0.0.0.0", "listen_port": port, "users": []any{map[string]any{"uuid": uuid}}}},
		"outbounds": []any{upstream, map[string]any{"type": "direct", "tag": "direct"}},
		"route": map[string]any{"rules": []any{map[string]any{
			"ip_cidr": []string{testIP + "/32"}, "action": "route", "outbound": "direct",
			"override_address": "127.0.0.1", "override_port": webPort,
		}}, "final": "up"},
	})
	os.WriteFile(filepath.Join(dir, "server.json"), b, 0o600)
	server := exec.Command(sb, "run", "-c", filepath.Join(dir, "server.json"))
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { server.Process.Kill(); server.Wait() }()
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port)); err == nil {
			c.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	sub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "vless://%s@%s:%d?type=tcp&security=none#test\n", uuid, hostIP, port)
	}))
	defer sub.Close()
	t.Setenv("ALBATROSS_SOURCES", sub.URL)

	// The test server is not encrypted, which the app leaves out by default.
	// Bypassing China's sites makes names under .cn resolve through the
	// network's resolver (fakeDNS) instead of through the VPN.
	os.MkdirAll(filepath.Join(dir, "data", "albatross"), 0o755)
	os.WriteFile(filepath.Join(dir, "data", "albatross", "settings.json"), []byte(`{"allow_plain": true, "secure_only": false, "bypass": "CN"}`), 0o644)
	dnsAddr := fakeDNS(t)
	p := &fakeVPN{t: t, up: make(chan struct{}, 1), down: make(chan struct{}, 1)}
	defer p.close()
	ui, err := Start(filepath.Join(dir, "data"), lib, dnsAddr, p)
	if err != nil {
		t.Fatal(err)
	}
	defer Stop()
	base, token, _ := strings.Cut(ui, "/#")
	call := func(method, path, body string, out any) {
		req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
		req.Header.Set("X-Token", token)
		resp, err := (&http.Client{Transport: &http.Transport{Proxy: nil}}).Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		if out != nil {
			json.NewDecoder(resp.Body).Decode(out)
		}
	}
	var state struct {
		Status   string `json:"status"`
		Mobile   bool   `json:"mobile"`
		Servers  int    `json:"servers"`
		Settings struct {
			Mode string `json:"mode"`
		} `json:"settings"`
	}
	deadline := time.Now().Add(60 * time.Second)
	for call("GET", "/api/state", "", &state); state.Servers == 0 && time.Now().Before(deadline); call("GET", "/api/state", "", &state) {
		time.Sleep(300 * time.Millisecond)
	}
	if state.Servers == 0 || !state.Mobile || state.Settings.Mode != "proxy" {
		for _, l := range eng.Logs() {
			t.Logf("%s %s", l.Level, l.Text)
		}
		t.Fatalf("state %+v", state)
	}
	call("POST", "/api/connect", `{"country":""}`, nil)
	select {
	case <-p.up:
	case <-time.After(60 * time.Second):
		t.Fatal("the engine never asked for the VPN")
	}
	// Android denies apps the netlink socket that sing-box's
	// auto_detect_interface needs, and sing-box then refuses to start, so
	// no config written on a phone may ask for it.
	configs, _ := filepath.Glob(filepath.Join(dir, "data", "albatross", "run", "*sing-box.json"))
	if len(configs) == 0 {
		t.Fatal("no core configs written")
	}
	for _, path := range configs {
		var cfg struct {
			Route map[string]any `json:"route"`
		}
		b, _ := os.ReadFile(path)
		if err := json.Unmarshal(b, &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg.Route["auto_detect_interface"] == true {
			t.Fatalf("%s asks for auto_detect_interface, which stops sing-box on Android", filepath.Base(path))
		}
	}

	// TCP into the tunnel: only the VPN server can reach testIP.
	direct := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil}}
	resp, err := direct.Get("http://" + testIP + "/")
	if err != nil {
		t.Fatalf("HTTP through the tunnel: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "hello through the VPN" {
		t.Fatalf("unexpected answer: %q", body)
	}
	t.Logf("HTTP through the tunnel: %q", body)

	// UDP DNS to the tunnel's resolver address, as Android apps send it. A
	// name of the bypassed region is answered by the network's resolver
	// (fakeDNS).
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "udp", "172.19.0.2:53")
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	addrs, err := r.LookupHost(ctx, "albatross-test.cn")
	if err != nil || len(addrs) == 0 || addrs[0] != testIP {
		t.Fatalf("DNS through the tunnel: %v %v", addrs, err)
	}
	t.Logf("DNS through the tunnel: albatross-test.cn = %v", addrs)

	call("POST", "/api/disconnect", "", nil)
	select {
	case <-p.down:
	case <-time.After(10 * time.Second):
		t.Fatal("disconnecting did not stop the VPN")
	}
}

// fakeVPN does what TunnelService does, with a Linux TUN device.
type fakeVPN struct {
	t        *testing.T
	mu       sync.Mutex
	fd       int
	up, down chan struct{}
}

const (
	tunName = "aztest0"
	testIP  = "203.0.113.10"
)

// fakeDNS answers every A question with testIP, standing in for the
// network's resolver. It returns its address.
func fakeDNS(t *testing.T) string {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			var m dnsmessage.Message
			if m.Unpack(buf[:n]) != nil || len(m.Questions) == 0 {
				continue
			}
			m.Header.Response, m.Header.RecursionAvailable = true, true
			if q := m.Questions[0]; q.Type == dnsmessage.TypeA {
				m.Answers = []dnsmessage.Resource{{
					Header: dnsmessage.ResourceHeader{Name: q.Name, Type: q.Type, Class: q.Class, TTL: 60},
					Body:   &dnsmessage.AResource{A: [4]byte(net.ParseIP(testIP).To4())},
				}}
			}
			if out, err := m.Pack(); err == nil {
				pc.WriteTo(out, from)
			}
		}
	}()
	return pc.LocalAddr().String()
}

func (p *fakeVPN) StartVPN() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fd > 0 {
		return
	}
	fd, err := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		p.t.Errorf("open tun: %v", err)
		return
	}
	var ifr [unix.IFNAMSIZ + 64]byte
	copy(ifr[:], tunName)
	*(*uint16)(unsafe.Pointer(&ifr[unix.IFNAMSIZ])) = unix.IFF_TUN | unix.IFF_NO_PI
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), unix.TUNSETIFF, uintptr(unsafe.Pointer(&ifr[0]))); errno != 0 {
		p.t.Errorf("TUNSETIFF: %v", errno)
		unix.Close(fd)
		return
	}
	for _, args := range [][]string{
		{"link", "set", tunName, "up"},
		{"addr", "add", "172.19.0.1/30", "dev", tunName},
		{"route", "add", testIP + "/32", "dev", tunName},
	} {
		if out, err := exec.Command("ip", args...).CombinedOutput(); err != nil {
			p.t.Errorf("ip %v: %v %s", args, err, out)
		}
	}
	p.fd = fd
	if err := StartTun(int32(fd)); err != nil {
		p.t.Errorf("StartTun: %v", err)
	}
	p.up <- struct{}{}
}

func (p *fakeVPN) StopVPN() {
	p.close()
	select {
	case p.down <- struct{}{}:
	default:
	}
}

func (p *fakeVPN) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fd > 0 {
		unix.Close(p.fd)
		p.fd = 0
	}
	exec.Command("ip", "link", "del", tunName).Run()
}

func (p *fakeVPN) OpenURL(string)   {}
func (p *fakeVPN) OpenVPNSettings() {}
func (p *fakeVPN) Quit()            {}
func (p *fakeVPN) ShareText(string) {}
func (p *fakeVPN) ShareApp()        {}
func (p *fakeVPN) LanAddrs() string { return "" }
