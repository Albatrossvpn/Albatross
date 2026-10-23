package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

// HTTPProxy is a small HTTP/HTTPS proxy that forwards everything to Tor's
// SOCKS port. Windows' system proxy (WinINet) only speaks HTTP, so this is
// what browsers and most apps use in "system proxy" mode.
type HTTPProxy struct {
	srv  *http.Server
	ln   net.Listener
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
	tr   *http.Transport
}

func socksDialer(socksAddr string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	d, _ := proxy.SOCKS5("tcp", socksAddr, nil, &net.Dialer{Timeout: 30 * time.Second})
	cd := d.(proxy.ContextDialer)
	return cd.DialContext
}

func startHTTPProxy(listenAddr, socksAddr string, log *LogBuffer) (*HTTPProxy, error) {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, err
	}
	p := &HTTPProxy{ln: ln, dial: socksDialer(socksAddr)}
	p.tr = &http.Transport{
		DialContext:         p.dial,
		Proxy:               nil,
		MaxIdleConns:        64,
		IdleConnTimeout:     60 * time.Second,
		TLSHandshakeTimeout: 30 * time.Second,
	}
	p.srv = &http.Server{Handler: p, ReadHeaderTimeout: 30 * time.Second}
	go func() {
		if err := p.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Add("http proxy: %v", err)
		}
	}()
	log.Add("HTTP proxy listening on %s", listenAddr)
	return p, nil
}

func (p *HTTPProxy) Close() {
	_ = p.srv.Close()
	p.tr.CloseIdleConnections()
}

var hopHeaders = []string{
	"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

func (p *HTTPProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	if r.URL.Host == "" || !r.URL.IsAbs() {
		http.Error(w, "This is the Orbot HTTP proxy.", http.StatusBadRequest)
		return
	}
	out := r.Clone(r.Context())
	out.RequestURI = ""
	for _, h := range hopHeaders {
		out.Header.Del(h)
	}
	resp, err := p.tr.RoundTrip(out)
	if err != nil {
		http.Error(w, "Tor: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for _, h := range hopHeaders {
		resp.Header.Del(h)
	}
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (p *HTTPProxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if !strings.Contains(host, ":") {
		host += ":443"
	}
	upstream, err := p.dial(r.Context(), "tcp", host)
	if err != nil {
		http.Error(w, "Tor: "+err.Error(), http.StatusBadGateway)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		upstream.Close()
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	client, buf, err := hj.Hijack()
	if err != nil {
		upstream.Close()
		return
	}
	_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	if buf != nil && buf.Reader.Buffered() > 0 {
		b, _ := buf.Peek(buf.Reader.Buffered())
		_, _ = upstream.Write(b)
	}
	relay(client, upstream)
}

// relay copies data both ways and closes both connections when done.
func relay(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		} else {
			_ = dst.SetReadDeadline(time.Now().Add(5 * time.Second))
		}
	}
	go cp(a, b)
	go cp(b, a)
	wg.Wait()
	a.Close()
	b.Close()
}
