package tester

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Albatrossvpn/vp/internal/node"
)

// DownloadEndpoints serve the bytes a speed test reads. Cloudflare's takes
// the size asked for ("%d"); the others are large fixed files read only as
// far as asked, for servers that cannot reach Cloudflare (Workers-based
// configs) or when its speed test refuses (HTTP 429).
var DownloadEndpoints = []string{
	"https://speed.cloudflare.com/__down?bytes=%d",
	"https://cachefly.cachefly.net/10mb.test",
	"https://proof.ovh.net/files/10Mb.dat",
}

// Answering a probe proves little: Iran's filtering lets the first few
// kilobytes of a flow through before it throttles or cuts it, and an
// overloaded free server answers a small request and then crawls. A server
// that passes its probe also has to carry a real download.
const (
	// speedFirstByte is the longest the first byte of the download may take.
	speedFirstByte = 4 * time.Second
	// speedGiveUp is how long a download runs before one far below the
	// required speed is given up on.
	speedGiveUp = 2 * time.Second
)

// SpeedExempt reports whether a node is never held to a speed: the DNS
// tunnel and the ping carrier move a few bytes at a time by design, and Tor
// is slow by nature; each is chosen for getting through, not for speed.
func SpeedExempt(n *node.Node) bool {
	return n == nil || n.DNSTunnel() || n.ICMP() || n.WPMode == "tor"
}

// Throughput downloads size bytes through proxy and returns the speed in
// bytes a second, timed from the first byte so the server's and the site's
// handshakes are left out. A download that stalls, breaks or has not
// finished by limit is measured on what arrived; an error means nothing
// did. need (bytes a second, 0 for none) lets a download far below it stop
// early: it fails anyway, and the wait would only slow the scan.
func Throughput(ctx context.Context, proxy *url.URL, size int, limit time.Duration, need float64) (float64, error) {
	return ThroughputFrom(ctx, DownloadEndpoints, proxy, size, limit, need)
}

// ThroughputFrom is Throughput from the given endpoints, tried in order.
func ThroughputFrom(ctx context.Context, endpoints []string, proxy *url.URL, size int, limit time.Duration, need float64) (float64, error) {
	var lastErr error
	start := time.Now()
	for i, endpoint := range endpoints {
		left := limit - time.Since(start)
		if i > 0 && left < time.Second {
			break // too little left to measure anything on another site
		}
		bps, got, err := download(ctx, proxy, endpoint, size, left, need)
		if err == nil || got > 0 {
			// Bytes came: the server was measured, whatever ended it.
			return bps, nil
		}
		lastErr = err
		if ctx.Err() != nil || isTimeout(err) {
			// A server that does not answer one site in time will not
			// answer the next any sooner.
			break
		}
	}
	if lastErr == nil {
		lastErr = errors.New("no download endpoint")
	}
	return 0, lastErr
}

// download is one try of Throughput. got is the bytes that arrived.
func download(ctx context.Context, proxy *url.URL, endpoint string, size int, limit time.Duration, need float64) (bps float64, got int64, err error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	target := endpoint
	if strings.Contains(endpoint, "%d") {
		target = fmt.Sprintf(endpoint, size)
	}
	tr := &http.Transport{
		Proxy:                 http.ProxyURL(proxy),
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   limit,
		ResponseHeaderTimeout: min(limit, speedFirstByte+3*time.Second),
		DialContext:           (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
		TLSClientConfig:       &tls.Config{RootCAs: RootCAs},
	}
	defer tr.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	if !strings.Contains(endpoint, "%d") {
		req.Header.Set("Range", "bytes=0-"+strconv.Itoa(size-1))
	}
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return 0, 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	buf := make([]byte, 32<<10)
	var first time.Time
	asked := time.Now()
	for got < int64(size) {
		n, rerr := resp.Body.Read(buf[:min(len(buf), size-int(got))])
		if n > 0 {
			if first.IsZero() {
				first = time.Now()
				if first.Sub(asked) > speedFirstByte {
					// As good as nothing: no page waits that long.
					return 0, int64(n), nil
				}
			}
			got += int64(n)
		}
		if rerr != nil {
			if got == 0 {
				return 0, 0, rerr
			}
			break // a cut or a stall that ran out the time: measured on what came
		}
		if el := time.Since(first); need > 0 && el >= speedGiveUp && float64(got)/el.Seconds() < need/3 {
			break
		}
	}
	return float64(got) / max(time.Since(first).Seconds(), 0.05), got, nil
}
