package node

import "testing"

func TestSurge(t *testing.T) {
	text := `[Proxy]
VM = vmess, 1.2.3.4, 443, username=` + uid + `, ws=true, ws-path=/ray, ws-headers=Host:a.example.com, tls=true, sni=a.example.com, skip-cert-verify=true
TR = trojan, 5.6.7.8, 443, password=secretpw, sni=t.example.com
SS = ss, 9.9.9.9, 8388, encrypt-method=chacha20-ietf-poly1305, password=sspw, obfs=tls, obfs-host=dl.example.com
PX = http, 2.2.2.2, 8080, user1, pass1
HY = hysteria2, 3.3.3.3, 443, password=hypw, sni=h.example.com
# not a server
DIRECT = direct
`
	var got []*Node
	EachText(text, func(n *Node) { got = append(got, n) })
	by := map[string]*Node{}
	for _, n := range got {
		by[n.Proto] = n
	}
	vm := by["vmess"]
	if vm == nil || vm.Server != "1.2.3.4" || vm.UUID != uid || vm.Transport != "ws" || vm.Path != "/ray" || vm.Host != "a.example.com" || vm.Security != "tls" || !vm.Insecure {
		t.Fatalf("vmess: %+v", vm)
	}
	if tr := by["trojan"]; tr == nil || tr.Password != "secretpw" || tr.Security != "tls" || tr.SNI != "t.example.com" {
		t.Fatalf("trojan: %+v", by["trojan"])
	}
	if ss := by["shadowsocks"]; ss == nil || ss.Method != "chacha20-ietf-poly1305" || ss.Plugin != "obfs-local" {
		t.Fatalf("ss: %+v", by["shadowsocks"])
	}
	if px := by["http"]; px == nil || px.Username != "user1" || px.Password != "pass1" {
		t.Fatalf("http positional user/pass: %+v", by["http"])
	}
	if hy := by["hysteria2"]; hy == nil || hy.Password != "hypw" || hy.SNI != "h.example.com" {
		t.Fatalf("hysteria2: %+v", by["hysteria2"])
	}
}
