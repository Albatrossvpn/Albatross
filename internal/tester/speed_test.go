package tester

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// slowProxy stands in for a server: it answers every request itself, as
// an HTTP proxy asked for an http:// address would. Each host has its own
// behaviour.
type slowProxy struct {
	// burst is sent at once; after it, rate bytes a second (0 = at once,
	// below 0 = nothing more).
	burst, rate map[string]int
	refuse      map[string]bool
}

func (p *slowProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.URL.Hostname()
	if p.refuse[host] {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	n := 10 << 20
	if b := r.URL.Query().Get("bytes"); b != "" {
		n, _ = strconv.Atoi(b)
	}
	if rg := r.Header.Get("Range"); rg != "" {
		end, _ := strconv.Atoi(strings.TrimPrefix(rg, "bytes=0-"))
		n = end + 1
		w.Header().Set("Content-Length", strconv.Itoa(n))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.Header().Set("Content-Length", strconv.Itoa(n))
	}
	chunk := make([]byte, 4<<10)
	sent, burst, rate := 0, p.burst[host], p.rate[host]
	for sent < n {
		k := min(n-sent, len(chunk))
		if sent >= burst {
			switch {
			case rate < 0:
				<-r.Context().Done()
				return
			case rate > 0:
				time.Sleep(time.Duration(float64(k) / float64(rate) * float64(time.Second)))
			}
		}
		if _, err := w.Write(chunk[:k]); err != nil {
			return
		}
		w.(http.Flusher).Flush()
		sent += k
	}
}

func withEndpoints(t *testing.T, list ...string) {
	old := DownloadEndpoints
	DownloadEndpoints = list
	t.Cleanup(func() { DownloadEndpoints = old })
}

// A server that carries data is measured at its speed; one that lets the
// first kilobytes through and then crawls, the way filtering throttles a
// flow, is measured on the crawl and given up on early; a speed test that
// refuses is replaced by the next endpoint.
func TestThroughput(t *testing.T) {
	p := &slowProxy{
		burst:  map[string]int{"fast.test": 0, "trickle.test": 48 << 10, "stall.test": 32 << 10},
		rate:   map[string]int{"fast.test": 0, "trickle.test": 8 << 10, "stall.test": -1},
		refuse: map[string]bool{"busy.test": true},
	}
	srv := httptest.NewServer(p)
	defer srv.Close()
	proxy, _ := url.Parse(srv.URL)
	ctx := t.Context()

	withEndpoints(t, "http://fast.test/__down?bytes=%d")
	bps, err := Throughput(ctx, proxy, 256<<10, 5*time.Second, 96<<10)
	if err != nil || bps < 1<<20 {
		t.Fatalf("fast server: %.0f B/s, %v", bps, err)
	}

	withEndpoints(t, "http://trickle.test/__down?bytes=%d")
	start := time.Now()
	bps, err = Throughput(ctx, proxy, 256<<10, 8*time.Second, 96<<10)
	if err != nil || bps >= 96<<10 {
		t.Fatalf("a burst then a trickle passed as %.0f B/s (%v)", bps, err)
	}
	if took := time.Since(start); took > speedGiveUp+2*time.Second {
		t.Fatalf("a hopeless download ran for %s", took)
	}

	withEndpoints(t, "http://stall.test/__down?bytes=%d")
	bps, err = Throughput(ctx, proxy, 256<<10, 3*time.Second, 96<<10)
	if err != nil || bps >= 96<<10 {
		t.Fatalf("a burst then nothing passed as %.0f B/s (%v)", bps, err)
	}

	withEndpoints(t, "http://busy.test/__down?bytes=%d", "http://fast.test/10mb.test")
	bps, err = Throughput(ctx, proxy, 256<<10, 5*time.Second, 96<<10)
	if err != nil || bps < 1<<20 {
		t.Fatalf("no fallback after a refusal: %.0f B/s, %v", bps, err)
	}

	withEndpoints(t, "http://busy.test/__down?bytes=%d")
	if _, err := Throughput(ctx, proxy, 256<<10, 5*time.Second, 96<<10); err == nil {
		t.Fatal("a refusal with nothing downloaded is no measurement")
	}
}
