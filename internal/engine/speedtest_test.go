package engine

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func withSpeedServer(t *testing.T, phase time.Duration) (up *atomic.Int64) {
	t.Helper()
	up = new(atomic.Int64)
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/__down", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("bytes"))
		buf := make([]byte, 32<<10)
		for n > 0 {
			k := min(n, len(buf))
			if _, err := w.Write(buf[:k]); err != nil {
				return
			}
			n -= k
			time.Sleep(time.Millisecond) // a line, not a memory copy: the phase ends it, not the size
		}
	})
	mux.HandleFunc("/__up", func(w http.ResponseWriter, r *http.Request) {
		n, _ := io.Copy(io.Discard, r.Body)
		up.Add(n)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	oldPhase, oldChunk, oldCap := speedPhase, speedUpChunk, speedCap
	oldDown, oldUp, oldPing := speedURL, speedUpURL, speedPingURL
	speedPhase, speedUpChunk, speedCap = phase, 2<<20, 1<<30
	speedURL, speedUpURL, speedPingURL = srv.URL+"/__down?bytes=", srv.URL+"/__up", srv.URL+"/ping"
	t.Cleanup(func() {
		speedPhase, speedUpChunk, speedCap = oldPhase, oldChunk, oldCap
		speedURL, speedUpURL, speedPingURL = oldDown, oldUp, oldPing
	})
	return up
}

// The three measurements, against a local server: ping and jitter from small
// requests, download for the phase, upload in requests of its own.
func TestSpeedMeasurements(t *testing.T) {
	up := withSpeedServer(t, 800*time.Millisecond)
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: false}}

	ping, jitter, err := speedPing(t.Context(), client)
	if err != nil || ping < 0 || jitter < 0 || ping > 500 {
		t.Fatalf("ping %d jitter %d: %v", ping, jitter, err)
	}

	var told atomic.Int64
	start := time.Now()
	var last float64
	if err := speedDownload(t.Context(), client, func(bps float64) { told.Add(1); last = bps }); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took < 600*time.Millisecond || took > 4*time.Second {
		t.Fatalf("a download of one phase took %v", took)
	}
	if last < 10e6 {
		t.Fatalf("download read %.0f bit/s from a local server", last)
	}
	if told.Load() == 0 {
		t.Fatal("the speed was never told")
	}

	last = 0
	start = time.Now()
	if err := speedUpload(t.Context(), client, func(bps float64) { last = bps }); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 4*time.Second {
		t.Fatalf("an upload of one phase took %v", took)
	}
	if last < 10e6 || up.Load() == 0 {
		t.Fatalf("upload read %.0f bit/s, the server got %d bytes", last, up.Load())
	}
}

func TestSpeedDownloadFailsOnBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", http.StatusForbidden) }))
	defer srv.Close()
	old := speedURL
	speedURL = srv.URL + "/?bytes="
	defer func() { speedURL = old }()
	if err := speedDownload(t.Context(), http.DefaultClient, func(float64) {}); err == nil {
		t.Fatal("a refused download is no measurement")
	}
}

func TestSpeedUploadStopsWhenCancelled(t *testing.T) {
	withSpeedServer(t, 5*time.Second)
	ctx, cancel := context.WithCancel(t.Context())
	go func() { time.Sleep(300 * time.Millisecond); cancel() }()
	start := time.Now()
	err := speedUpload(ctx, http.DefaultClient, func(float64) {})
	if err == nil {
		t.Fatal("a stopped upload reports no error")
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("stopping took %v", took)
	}
}

// The rate leaves out a transfer's first second: a burst while buffers fill
// must not read as the line's speed.
func TestRateLeavesOutTheStart(t *testing.T) {
	old := speedPhase
	speedPhase = 8 * time.Second
	defer func() { speedPhase = old }()
	var s []rateSample
	// 2 MB in the first half second, then 1 Mb/s (125000 bytes a second).
	s = append(s, rateSample{0, 0}, rateSample{500 * time.Millisecond, 2_000_000})
	n := int64(2_000_000)
	for at := 600 * time.Millisecond; at <= 8*time.Second; at += 100 * time.Millisecond {
		n += 12_500
		s = append(s, rateSample{at, n})
	}
	got := rateOf(s)
	if got < 0.95e6 || got > 1.05e6 {
		t.Fatalf("rate %.0f bit/s, want about 1000000", got)
	}
	if rateOf(nil) != 0 {
		t.Fatal("no samples, a rate")
	}
	// Under two seconds: the whole average.
	if got := rateOf([]rateSample{{0, 0}, {time.Second, 125_000}}); got < 0.99e6 || got > 1.01e6 {
		t.Fatalf("short transfer: %.0f", got)
	}
}

// A fast line does not use data for nothing: an upload stops at the cap, a
// request after the one that reached it.
func TestSpeedUploadStopsAtTheCap(t *testing.T) {
	up := withSpeedServer(t, 5*time.Second)
	speedCap = 5 << 20 // after withSpeedServer: its cleanup restores it
	start := time.Now()
	if err := speedUpload(t.Context(), http.DefaultClient, func(float64) {}); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("an upload that reached the cap took %v of a 5 s phase", took)
	}
	if got := up.Load(); got < speedCap || got > speedCap+speedUpChunk {
		t.Fatalf("the server got %d bytes, want from %d to %d", got, speedCap, speedCap+speedUpChunk)
	}
}
