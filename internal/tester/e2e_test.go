package tester

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Albatrossvpn/vp/internal/core"
	"github.com/Albatrossvpn/vp/internal/node"
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
	ports, _ := core.FreePorts(16)
	dir := t.TempDir()
	uuid := "b831381d-6324-4d53-ad4f-8cda48b30811"
	certPath, keyPath := certFiles(t, dir, "test.example")
	tlsIn := func(alpn ...string) map[string]any {
		t := map[string]any{"enabled": true, "server_name": "test.example", "certificate_path": certPath, "key_path": keyPath}
		if len(alpn) > 0 {
			t["alpn"] = alpn
		}
		return t
	}
	realityPriv, realityPub := x25519Keys(base64.RawURLEncoding)
	wgSrvPriv, wgSrvPub := x25519Keys(base64.StdEncoding)
	wgCliPriv, wgCliPub := x25519Keys(base64.StdEncoding)
	ss22 := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))

	serverSB := map[string]any{
		"log": map[string]any{"level": "warn"},
		"inbounds": []any{
			map[string]any{"type": "vless", "listen": "0.0.0.0", "listen_port": ports[0], "users": []any{map[string]any{"uuid": uuid}}},
			map[string]any{"type": "shadowsocks", "listen": "0.0.0.0", "listen_port": ports[1], "method": "aes-128-gcm", "password": "secret"},
			map[string]any{"type": "vmess", "listen": "0.0.0.0", "listen_port": ports[2], "users": []any{map[string]any{"uuid": uuid}},
				"transport": map[string]any{"type": "ws", "path": "/ws"}},
			map[string]any{"type": "trojan", "listen": "0.0.0.0", "listen_port": ports[5], "users": []any{map[string]any{"password": "secret"}}, "tls": tlsIn()},
			map[string]any{"type": "hysteria2", "listen": "0.0.0.0", "listen_port": ports[6], "users": []any{map[string]any{"password": "secret"}}, "tls": tlsIn("h3")},
			map[string]any{"type": "tuic", "listen": "0.0.0.0", "listen_port": ports[7], "users": []any{map[string]any{"uuid": uuid, "password": "secret"}}, "congestion_control": "bbr", "tls": tlsIn("h3")},
			map[string]any{"type": "anytls", "listen": "0.0.0.0", "listen_port": ports[8], "users": []any{map[string]any{"password": "secret"}}, "tls": tlsIn()},
			map[string]any{"type": "socks", "listen": "0.0.0.0", "listen_port": ports[9], "users": []any{map[string]any{"username": "user", "password": "pass"}}},
			map[string]any{"type": "hysteria", "listen": "0.0.0.0", "listen_port": ports[11], "up_mbps": 100, "down_mbps": 100, "users": []any{map[string]any{"auth_str": "secret"}}, "tls": tlsIn("hysteria")},
			map[string]any{"type": "shadowsocks", "listen": "0.0.0.0", "listen_port": ports[12], "method": "2022-blake3-aes-128-gcm", "password": ss22},
			map[string]any{"type": "vless", "listen": "0.0.0.0", "listen_port": ports[13], "users": []any{map[string]any{"uuid": uuid, "flow": "xtls-rprx-vision"}},
				"tls": map[string]any{"enabled": true, "server_name": "www.microsoft.com", "reality": map[string]any{"enabled": true,
					"handshake": map[string]any{"server": "www.microsoft.com", "server_port": 443}, "private_key": realityPriv, "short_id": []string{"0123456789abcdef"}}}},
			map[string]any{"type": "vless", "listen": "0.0.0.0", "listen_port": ports[15], "users": []any{map[string]any{"uuid": uuid}},
				"tls": tlsIn("h2"), "transport": map[string]any{"type": "grpc", "service_name": "g"}},
		},
		// A WireGuard endpoint is a server too: the peer's packets are routed
		// like any inbound's.
		"endpoints": []any{map[string]any{"type": "wireguard", "tag": "wg-in", "listen_port": ports[10], "address": []string{"10.7.0.1/32"},
			"private_key": wgSrvPriv, "peers": []any{map[string]any{"public_key": wgCliPub, "allowed_ips": []string{"10.7.0.2/32"}}}}},
		"outbounds": []any{upstream},
	}
	serverXray := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{
			"listen": "0.0.0.0", "port": ports[3], "protocol": "vless",
			"settings":       map[string]any{"clients": []any{map[string]any{"id": uuid}}, "decryption": "none"},
			"streamSettings": map[string]any{"network": "xhttp", "xhttpSettings": map[string]any{"path": "/x", "mode": "auto"}},
		}, map[string]any{
			"listen": "0.0.0.0", "port": ports[14], "protocol": "vless",
			"settings":       map[string]any{"clients": []any{map[string]any{"id": uuid}}, "decryption": "none"},
			"streamSettings": map[string]any{"network": "kcp"},
		}},
		"outbounds": []any{xrayUpstream},
	}
	startServer(t, sb, dir, "server-sb.json", serverSB, "run", "-c")
	startServer(t, xr, dir, "server-xray.json", serverXray, "run", "-c")
	// Wait for every server to listen; Xray can take longer than a fixed
	// pause when the machine is busy. ports[4] is deliberately dead; the
	// UDP servers cannot be checked this way.
	for _, p := range []int{ports[0], ports[1], ports[2], ports[3], ports[5], ports[8], ports[9], ports[12], ports[13], ports[15]} {
		for i := 0; i < 100; i++ {
			if c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(p)); err == nil {
				c.Close()
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	at := func(i int) string { return hostIP + ":" + strconv.Itoa(ports[i]) }
	links := map[string]bool{
		"trojan://secret@" + at(5) + "?security=tls&sni=test.example&allowInsecure=1&type=tcp#trojan":                                                                                             true,
		"hysteria2://secret@" + at(6) + "?sni=test.example&insecure=1#hy2":                                                                                                                        true,
		"tuic://" + uuid + ":secret@" + at(7) + "?sni=test.example&alpn=h3&congestion_control=bbr&insecure=1#tuic":                                                                                true,
		"anytls://secret@" + at(8) + "?sni=test.example&insecure=1#anytls":                                                                                                                        true,
		"socks://user:pass@" + at(9) + "#socks":                                                                                                                                                   true,
		"wireguard://" + url.QueryEscape(wgCliPriv) + "@" + at(10) + "?publickey=" + url.QueryEscape(wgSrvPub) + "&address=10.7.0.2/32&mtu=1280#wg":                                               true,
		"hysteria://" + at(11) + "?auth=secret&peer=test.example&insecure=1&upmbps=10&downmbps=50#hy1":                                                                                            true,
		"ss://2022-blake3-aes-128-gcm:" + url.QueryEscape(ss22) + "@" + at(12) + "#ss22":                                                                                                          true,
		"vless://" + uuid + "@" + at(13) + "?security=reality&sni=www.microsoft.com&fp=chrome&pbk=" + realityPub + "&sid=0123456789abcdef&type=tcp&flow=xtls-rprx-vision&encryption=none#reality": true,
		"vless://" + uuid + "@" + at(14) + "?type=kcp&headerType=none&security=none&encryption=none#kcp":                                                                                          true,
		"vless://" + uuid + "@" + at(15) + "?type=grpc&serviceName=g&security=tls&sni=test.example&allowInsecure=1&encryption=none#grpc":                                                          true,
		"vless://" + uuid + "@" + hostIP + ":" + strconv.Itoa(ports[0]) + "?type=tcp&security=none#vless":                                                                                         true,
		"ss://YWVzLTEyOC1nY206c2VjcmV0@" + hostIP + ":" + strconv.Itoa(ports[1]) + "#ss":                                                                                                          true,
		"vless://" + uuid + "@" + hostIP + ":" + strconv.Itoa(ports[3]) + "?type=xhttp&path=%2Fx&mode=auto&security=none&encryption=none#xh":                                                      true,
		"vless://" + uuid + "@" + hostIP + ":" + strconv.Itoa(ports[4]) + "?type=tcp&security=none#dead":                                                                                          false,
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
		if !n.UDPBased() && (probes[i] > 0) != want[n] {
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

// certFiles writes a self-signed certificate and key for name.
func certFiles(t *testing.T, dir, name string) (cert, key string) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalPKCS8PrivateKey(priv)
	cert, key = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb}), 0o600)
	return cert, key
}

// x25519Keys returns a private and public key, as REALITY and WireGuard
// write them.
func x25519Keys(enc *base64.Encoding) (priv, pub string) {
	k, _ := ecdh.X25519().GenerateKey(rand.Reader)
	return enc.EncodeToString(k.Bytes()), enc.EncodeToString(k.PublicKey().Bytes())
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
