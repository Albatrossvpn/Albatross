package engine

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The contents API sends the file only when asked for it raw, a stalled
// download gives way to the next address, and one that keeps moving is
// given its time but not the next address's.
func TestDownload(t *testing.T) {
	defer func(i, m time.Duration) { downloadIdle, downloadMax = i, m }(downloadIdle, downloadMax)
	downloadIdle, downloadMax = 150*time.Millisecond, 600*time.Millisecond
	ctx := context.Background()

	var accept string
	api := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		accept = r.Header.Get("Accept")
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("SRS")), Request: r}, nil
	})}
	if b, err := download(ctx, api, "https://api.github.com/repos/o/r/contents/a.srs?ref=rules", 1<<20); err != nil || string(b) != "SRS" || accept != "application/vnd.github.raw" {
		t.Fatalf("api: %q %v, Accept %q", b, err, accept)
	}

	trickle := func(every time.Duration, stallAfter int) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for i := 0; ; i++ {
				if i == stallAfter {
					<-r.Context().Done()
					return
				}
				if _, err := w.Write(bytes.Repeat([]byte("x"), 100)); err != nil {
					return
				}
				w.(http.Flusher).Flush()
				select {
				case <-time.After(every):
				case <-r.Context().Done():
					return
				}
			}
		}))
	}
	stalled := trickle(10*time.Millisecond, 3)
	defer stalled.Close()
	start := time.Now()
	if _, err := download(ctx, http.DefaultClient, stalled.URL, 1<<20); err == nil || errors.Is(err, errSlow) {
		t.Fatalf("stalled: %v", err)
	}
	if d := time.Since(start); d > 400*time.Millisecond {
		t.Fatalf("a stalled download took %v", d)
	}
	slow := trickle(50*time.Millisecond, -1)
	defer slow.Close()
	if _, err := download(ctx, http.DefaultClient, slow.URL, 1<<20); !errors.Is(err, errSlow) {
		t.Fatalf("slow: %v", err)
	}
}

// The ad list is left alone while ads are not blocked.
func TestUpdateRulesSkips(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	c := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		asked = append(asked, r.URL.String())
		mu.Unlock()
		return nil, errors.New("offline")
	})}
	client := func() *http.Client { return c }
	updateRules(context.Background(), t.TempDir(), client, client, func(name string) bool { return name != "ads.srs" })
	if len(asked) == 0 {
		t.Fatal("nothing was asked for")
	}
	for _, u := range asked {
		if strings.Contains(u, "/ads.srs") {
			t.Fatalf("asked for %s", u)
		}
	}
}

// A slow host gives way to one more; when that one is slow too, the list
// is left for rulesBackoff instead of being fetched at every connection.
func TestUpdateRulesSlow(t *testing.T) {
	defer func(i, m time.Duration) { downloadIdle, downloadMax = i, m }(downloadIdle, downloadMax)
	downloadIdle, downloadMax = 150*time.Millisecond, 300*time.Millisecond
	ads := func(name string) bool { return name == "ads.srs" }
	good := "SRS" + strings.Repeat("x", 100)
	for _, tc := range []struct {
		name  string
		fast  int // the request that gets a whole list at once, from 1
		asked int
		list  string
	}{
		{"both slow", 0, 2, "SRS shipped"},
		{"second fast", 2, 2, good},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "ads.srs")
			os.WriteFile(path, []byte("SRS shipped"), 0o644)
			os.Chtimes(path, time.Unix(0, 0), time.Unix(0, 0))
			var mu sync.Mutex
			var hosts []string
			c := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				mu.Lock()
				hosts = append(hosts, r.URL.Host)
				n := len(hosts)
				mu.Unlock()
				var body io.Reader = strings.NewReader(good)
				if n != tc.fast {
					body = trickleBody{r.Context()}
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(body), Request: r}, nil
			})}
			client := func() *http.Client { return c }
			updateRules(context.Background(), dir, client, client, ads)
			if len(hosts) != tc.asked || len(hosts) == 2 && hosts[0] == hosts[1] {
				t.Fatalf("asked %v", hosts)
			}
			b, _ := os.ReadFile(path)
			st, err := os.Stat(path)
			if err != nil || string(b) != tc.list {
				t.Fatalf("list %q, %v", b, err)
			}
			age := time.Since(st.ModTime())
			if tc.fast == 0 && (age < 24*time.Hour-rulesBackoff-time.Minute || age > 24*time.Hour-rulesBackoff+time.Minute) || tc.fast != 0 && age > time.Minute {
				t.Fatalf("the list is %v old", age)
			}
			hosts = nil
			updateRules(context.Background(), dir, client, client, ads)
			if len(hosts) != 0 {
				t.Fatalf("asked again: %v", hosts)
			}
		})
	}
}

// trickleBody sends a few bytes at a time until its request is over.
type trickleBody struct{ ctx context.Context }

func (b trickleBody) Read(p []byte) (int, error) {
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-time.After(20 * time.Millisecond):
		return copy(p, "xxxxxxxxxx"), nil
	}
}

// With ads blocked, programs send the region's sites through the proxy too,
// so its ad networks reach the core.
func TestProxyBypass(t *testing.T) {
	has := func(l []string, s string) bool {
		for _, x := range l {
			if x == s {
				return true
			}
		}
		return false
	}
	if l := proxyBypass("IR", false); !has(l, "*.ir") || !has(l, "aparatcdn.com") || !has(l, "*.aparatcdn.com") || !has(l, "192.168.*") {
		t.Fatalf("ads allowed: %v", l)
	}
	lan := proxyBypass("", false)
	if l := proxyBypass("IR", true); len(l) != len(lan) || has(l, "*.ir") || !has(l, "<local>") {
		t.Fatalf("ads blocked: %v", l)
	}
}

// An old Chocolate4U download goes, the shipped list takes its place and is
// replaced at the first chance, and no region's own site is in CI's list of
// names the ad list must never block.
func TestInstallAdList(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "geosite-ads.srs"), []byte("SRS old"), 0o644)
	if err := installRules(dir); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(dir, "geosite-ads.srs")) {
		t.Fatal("the old list is still there")
	}
	st, err := os.Stat(filepath.Join(dir, "ads.srs"))
	if err != nil || time.Since(st.ModTime()) < 24*time.Hour {
		t.Fatalf("shipped list: %v", err)
	}
	b, err := os.ReadFile("../../build/adblock/canary.txt")
	if err != nil {
		t.Fatal(err)
	}
	canary := map[string]bool{}
	for _, l := range strings.Split(string(b), "\n") {
		canary[strings.TrimSpace(l)] = true
	}
	for _, r := range regions {
		for _, n := range r.Extra {
			if !canary[n] {
				t.Errorf("%s (a site of %s) is not in build/adblock/canary.txt", n, r.Code)
			}
		}
	}
}
