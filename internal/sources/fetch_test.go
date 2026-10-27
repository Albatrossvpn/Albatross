package sources

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Albatrossvpn/vp/internal/node"
)

func TestDatedDays(t *testing.T) {
	// 20:00 UTC is already the next day in China.
	days := datedDays(time.Date(2026, 10, 27, 20, 0, 0, 0, time.UTC))
	got := []string{}
	for _, d := range days {
		got = append(got, datedURL("{YYYY}/{MM}/{YYYY}{MM}{DD}", d))
	}
	want := []string{"2026/10/20261028", "2026/10/20261027", "2026/10/20261027"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("days = %v, want %v", got, want)
	}
}

func TestFetchDatedTakesTheNewestDayThatAnswers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/uploads/2026/10/1-20261026.txt" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, "vless://11111111-1111-1111-1111-111111111111@192.0.2.7:443?security=tls#a\n")
	}))
	defer srv.Close()
	f := &Fetcher{
		Direct:   srv.Client(),
		CacheDir: t.TempDir(),
		Now:      func() time.Time { return time.Date(2026, 10, 27, 3, 0, 0, 0, time.UTC) },
	}
	s := Source{ID: "d", Kind: KindSubscription, URL: srv.URL + "/uploads/{YYYY}/{MM}/1-{YYYY}{MM}{DD}.txt", Enabled: true}
	res := f.Fetch(context.Background(), s)
	if res.Err != nil || !strings.Contains(res.Text, "192.0.2.7") {
		t.Fatalf("got err %v text %q", res.Err, res.Text)
	}
	if res.Source.URL != s.URL {
		t.Fatalf("the result names %q, want the address with the date left in", res.Source.URL)
	}
	// Nothing answers any more: the copy on disk is served, with the error.
	srv.Config.Handler = http.NotFoundHandler()
	res = f.Fetch(context.Background(), s)
	if res.Err == nil || !res.FromCache || !strings.Contains(res.Text, "192.0.2.7") {
		t.Fatalf("got err %v cache %v text %q", res.Err, res.FromCache, res.Text)
	}
}

func TestFeedLinksOf(t *testing.T) {
	feed := `<feed><entry><title>one</title><media:description>Nodes: https://paste.example/raw/new.
Player https://www.youtube.com/watch?v=x and https://github.com/o/r/releases and https://short.example/x?a=1&amp;b=2</media:description></entry>
<entry><media:description>https://paste.example/raw/old https://paste.example/raw/new</media:description></entry></feed>`
	got := feedLinksOf(feed)
	want := []string{"https://paste.example/raw/new", "https://short.example/x?a=1&b=2", "https://paste.example/raw/old"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("links = %v, want %v", got, want)
	}
	if got := feedLinksOf(`<rss><channel><item><description>&lt;a href="https://x.example/l.txt"&gt;l&lt;/a&gt;</description></item></channel></rss>`); len(got) != 1 || got[0] != "https://x.example/l.txt" {
		t.Fatalf("rss links = %v", got)
	}
	var many strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&many, "<entry>https://p.example/%d </entry>", i)
	}
	if got := feedLinksOf(many.String()); len(got) != feedEntries {
		t.Fatalf("%d links from %d entries, want only the newest %d", len(got), 20, feedEntries)
	}
}

func TestFetchFeedFollowsTheLinksOneHop(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feed.xml":
			fmt.Fprintf(w, `<feed><entry><media:description>%s/paste/a %s/go/b %s/gone %s/pic.png</media:description></entry></feed>`, srv.URL, srv.URL, srv.URL, srv.URL)
		case "/paste/a":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "vless://11111111-1111-1111-1111-111111111111@192.0.2.7:443?security=tls#a\n")
		case "/go/b":
			http.Redirect(w, r, "/xray?config="+"vless%3A%2F%2F22222222-2222-2222-2222-222222222222%40192.0.2.8%3A443%3Fsecurity%3Dtls", http.StatusFound)
		case "/xray":
			fmt.Fprint(w, "<html>a page that holds no list</html>")
		case "/pic.png":
			w.Header().Set("Content-Type", "image/png")
			fmt.Fprint(w, "png")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	f := &Fetcher{Direct: srv.Client(), CacheDir: t.TempDir()}
	s := Source{ID: "f", Kind: KindFeed, URL: srv.URL + "/feed.xml", Enabled: true}
	res := f.Fetch(context.Background(), s)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	for _, want := range []string{"192.0.2.7", "192.0.2.8"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("the text lacks %s: %q", want, res.Text)
		}
	}
	if strings.Contains(res.Text, "png") {
		t.Errorf("an image was read: %q", res.Text)
	}
	// The feed answers but no link does: the error, and the copy on disk.
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/feed.xml" {
			fmt.Fprintf(w, `<feed><entry>%s/gone</entry></feed>`, srv.URL)
			return
		}
		http.NotFound(w, r)
	})
	res = f.Fetch(context.Background(), s)
	if res.Err == nil || !res.FromCache || !strings.Contains(res.Text, "192.0.2.7") {
		t.Fatalf("got err %v cache %v text %q", res.Err, res.FromCache, res.Text)
	}
}

// A LEAP provider answers with JSON and PEM, not a channel preview: the
// three answers are joined into one document the parser reads.
func TestFetchLEAP(t *testing.T) {
	eip := `{"gateways":[{"capabilities":{"transport":[{"ports":["53"],"protocols":["udp"],"type":"openvpn"}]},"host":"vpn01-sea.riseup.net","ip_address":"192.0.2.9","location":"Seattle"}],"locations":{"Seattle":{"country_code":"US"}},"openvpn_configuration":{"auth":"SHA512","cipher":"AES-256-GCM"},"version":3}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/config/eip-service.json":
			fmt.Fprint(w, eip)
		case "/3/cert":
			fmt.Fprint(w, "-----BEGIN RSA PRIVATE KEY-----\nMIIE\n-----END RSA PRIVATE KEY-----\n-----BEGIN CERTIFICATE-----\nMIID\n-----END CERTIFICATE-----\n")
		case "/ca.crt":
			fmt.Fprint(w, "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	leapProviders[srv.URL] = srv.URL + "/ca.crt"
	defer delete(leapProviders, srv.URL)
	f := &Fetcher{Direct: srv.Client(), CacheDir: t.TempDir()}
	res := f.Fetch(context.Background(), Source{ID: "l", Kind: KindLEAP, URL: srv.URL, Enabled: true})
	if res.Err != nil {
		t.Fatalf("fetch: %v", res.Err)
	}
	got := node.ParseLEAP(res.Text)
	if len(got) != 1 || got[0].Node.Server != "192.0.2.9" || got[0].Node.Port != 53 {
		t.Fatalf("parsed %d servers from %q", len(got), res.Text)
	}
}
