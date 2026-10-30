package node

import "testing"

func TestQuantumultX(t *testing.T) {
	text := `[server_local]
vmess=1.2.3.4:443, method=auto, password=` + uid + `, obfs=wss, obfs-host=a.example.com, obfs-uri=/ray, tls-verification=false, tag=VM WS TLS
trojan=5.6.7.8:443, password=secretpw, over-tls=true, tls-host=t.example.com, tag=TR
shadowsocks=9.9.9.9:8388, method=chacha20-ietf-poly1305, password=sspw, obfs=http, obfs-host=dl.example.com, tag=SS OBFS
vless=4.3.2.1:443, method=none, password=` + uid + `, obfs=grpc, obfs-uri=grpcsvc, over-tls=true, tls-host=v.example.com, tag=VL GRPC
# a comment, and a line that is not a server
something=else
`
	var got []*Node
	EachText(text, func(n *Node) { got = append(got, n) })
	if len(got) != 4 {
		t.Fatalf("got %d nodes, want 4: %+v", len(got), got)
	}
	by := map[string]*Node{}
	for _, n := range got {
		by[n.Proto] = n
	}
	vm := by["vmess"]
	if vm == nil || vm.Server != "1.2.3.4" || vm.Port != 443 || vm.UUID != uid || vm.Transport != "ws" || vm.Host != "a.example.com" || vm.Path != "/ray" || vm.Security != "tls" || !vm.Insecure {
		t.Fatalf("vmess: %+v", vm)
	}
	tr := by["trojan"]
	if tr == nil || tr.Password != "secretpw" || tr.Security != "tls" || tr.SNI != "t.example.com" {
		t.Fatalf("trojan: %+v", tr)
	}
	ss := by["shadowsocks"]
	if ss == nil || ss.Method != "chacha20-ietf-poly1305" || ss.Password != "sspw" || ss.Plugin != "obfs-local" {
		t.Fatalf("shadowsocks: %+v", ss)
	}
	vl := by["vless"]
	if vl == nil || vl.UUID != uid || vl.Transport != "grpc" || vl.ServiceName != "grpcsvc" || vl.Security != "tls" {
		t.Fatalf("vless: %+v", vl)
	}
}
