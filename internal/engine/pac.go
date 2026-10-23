package engine

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Devices sharing this connection (Settings.AllowLAN) can be pointed at an
// automatic proxy setup address instead of typing an address and port:
// Android, iOS, Windows and macOS all accept one in their proxy settings.
// It is served on the port after the proxy's while sharing is on.

// pacPort is the port the setup file is served on for a proxy port.
func pacPort(proxyPort string) string {
	p, _ := strconv.Atoi(proxyPort)
	return strconv.Itoa(p + 1)
}

// servePAC serves the setup file until ctx ends. Each device is told the
// address it reached this one at, so the file works on every network
// this device is on.
func servePAC(ctx context.Context, proxyAddr string) {
	_, port, err := net.SplitHostPort(proxyAddr)
	if err != nil {
		return
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", pacPort(port)))
	if err != nil {
		return
	}
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/proxy.pac" && r.URL.Path != "/wpad.dat" {
			http.NotFound(w, r)
			return
		}
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		proxy := net.JoinHostPort(host, port)
		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		fmt.Fprintf(w, `function FindProxyForURL(url, host) {
  if (isPlainHostName(host) || shExpMatch(host, "*.local") ||
      isInNet(host, "10.0.0.0", "255.0.0.0") || isInNet(host, "172.16.0.0", "255.240.0.0") ||
      isInNet(host, "192.168.0.0", "255.255.0.0") || isInNet(host, "127.0.0.0", "255.0.0.0"))
    return "DIRECT";
  return "PROXY %s; SOCKS5 %s";
}
`, proxy, proxy)
	})}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	srv.Serve(ln)
}
