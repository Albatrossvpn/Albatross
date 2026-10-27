package core

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Albatrossvpn/vp/internal/node"
)

// The real helpers, started as the app starts them, take only the run's
// login on their SOCKS5 port. ALBATROSS_HELPERS names a folder holding
// them (the build's test job fills one); without it the test is skipped.
func TestHelperPortsTakeOnlyTheLogin(t *testing.T) {
	dir := os.Getenv("ALBATROSS_HELPERS")
	if dir == "" {
		t.Skip("ALBATROSS_HELPERS not set")
	}
	r := &Runtime{Bin: Locate(dir), Dir: t.TempDir()}
	// What a helper says goes in the log, for when one fails here.
	r.OnLine = func(name, line string) { t.Logf("%s| %s", name, line) }
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// NaiveProxy answers its first client only once it has loaded the
	// server's page (its preamble, which makes it look like a browser);
	// at an address that drops packets that takes minutes to fail, at a
	// closed port no time.
	closed, err := FreePorts(1)
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range []string{
		"naive+https://u:p@127.0.0.1:" + strconv.Itoa(closed[0]) + "#N",
		"juicity://11111111-1111-1111-1111-111111111111:pw@192.0.2.1:443?sni=a.example#J",
		"mieru://u:p@192.0.2.1:2086?protocol=tcp#M",
		"wstunnel://192.0.2.1:443?path=secret#W",
		"amneziawg://cPr%2BXL8mZ2rA2lD8Q1D8Q1D8Q1D8Q1D8Q1D8Q1D8Q1E%3D@192.0.2.1:8447?publickey=HIgo9xNzJMWLKASShiTqIybxZ0U3wGLiUeJ1PKf8ykw%3D&address=10.8.0.2%2F32&jc=4&jmin=40&jmax=70#A",
	} {
		n, err := node.ParseLink(link)
		if err != nil {
			t.Fatalf("%s: %v", link, err)
		}
		name := n.Sidecar()
		if r.Bin.Sidecar(name) == "" {
			t.Errorf("%s: not in %s", name, dir)
			continue
		}
		ports, err := FreePorts(2)
		if err != nil {
			t.Fatal(err)
		}
		// Without a login the port answers anyone: the helper works at
		// all, whatever the login does.
		if p := r.startSidecar(ctx, n, ports[1], ""); p == nil {
			t.Errorf("%s did not start without a login", name)
		} else {
			if a := "127.0.0.1:" + strconv.Itoa(ports[1]); !socksNoAuth(a) {
				t.Errorf("%s: without a login its port does not answer a client (%s)", name, socksTrace(a, "", ""))
			}
			p.Stop()
		}
		pass := helperPass()
		p := r.startSidecar(ctx, n, ports[0], pass)
		if p == nil {
			t.Errorf("%s did not start with a login on its port", name)
			continue
		}
		addr := "127.0.0.1:" + strconv.Itoa(ports[0])
		if socksNoAuth(addr) {
			t.Errorf("%s: its port takes a client without the login", name)
		}
		if socksLogin(addr, node.SocksUser, "wrong-"+pass) {
			t.Errorf("%s: its port takes a wrong password", name)
		}
		if !socksLogin(addr, node.SocksUser, pass) {
			t.Errorf("%s: its port refuses the run's login (%s)", name, socksTrace(addr, node.SocksUser, pass))
		}
		p.Stop()
	}
}

// socksNoAuth reports whether a SOCKS5 server at addr accepts a client
// that offers no authentication.
func socksNoAuth(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return false
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write([]byte{5, 1, 0}); err != nil {
		return false
	}
	b := make([]byte, 2)
	if _, err := io.ReadFull(c, b); err != nil {
		return false
	}
	return b[0] == 5 && b[1] == 0
}

// socksLogin reports whether a SOCKS5 server at addr takes user and pass
// (RFC 1929).
func socksLogin(addr, user, pass string) bool {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return false
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write([]byte{5, 1, 2}); err != nil {
		return false
	}
	b := make([]byte, 2)
	if _, err := io.ReadFull(c, b); err != nil || b[0] != 5 || b[1] != 2 {
		return false
	}
	req := append([]byte{1, byte(len(user))}, user...)
	req = append(append(req, byte(len(pass))), pass...)
	if _, err := c.Write(req); err != nil {
		return false
	}
	if _, err := io.ReadFull(c, b); err != nil {
		return false
	}
	return b[1] == 0
}

// socksTrace says what a SOCKS5 server at addr answers to a login, byte
// by byte, for the log.
func socksTrace(addr, user, pass string) string {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return "dial: " + err.Error()
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	method := byte(2)
	if user == "" {
		method = 0
	}
	c.Write([]byte{5, 1, method})
	b := make([]byte, 64)
	k, err := io.ReadAtLeast(c, b, 2)
	out := fmt.Sprintf("greeting answered % x", b[:k])
	if err != nil || method == 0 {
		if err != nil {
			out += ", then " + err.Error()
		}
		return out
	}
	req := append([]byte{1, byte(len(user))}, user...)
	c.Write(append(append(req, byte(len(pass))), pass...))
	k, err = io.ReadAtLeast(c, b, 2)
	out += fmt.Sprintf(", login answered % x", b[:k])
	if err != nil {
		out += ", then " + err.Error()
	}
	return out
}
