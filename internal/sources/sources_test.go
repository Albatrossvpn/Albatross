package sources

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
)

// The own-servers list opens only with the key it was sealed with, and is
// offered only when the app was built with one.
func TestSealedOwnList(t *testing.T) {
	defer func(k string) { OwnKey = k }(OwnKey)
	OwnKey = ""
	for _, s := range Central(7) {
		if s.Sealed {
			t.Fatal("own list offered without a key")
		}
	}
	OwnKey = strings.Repeat("ab", 32)
	c := Central(7)
	if !c[0].Sealed || !c[0].Relay || !strings.HasSuffix(c[0].URL, "/own-0.bin"+KeyedSuffix) || !c[0].Keyed {
		t.Fatalf("the relay list must come first for every install: %+v", c[0])
	}
	if !c[1].Sealed || !c[2].Sealed || c[1].Relay || c[1].URL == c[2].URL || !strings.Contains(c[1].URL, "/own-") {
		t.Fatalf("own lists missing: %+v", c[1:3])
	}
	if SnapshotFor(c[1]) != "" {
		t.Fatal("a sealed bucket has no plaintext snapshot")
	}
	// Every install reads two different parts, and every part is read.
	seen := map[string]bool{}
	for i := range uint32(64) {
		c := Central(i)
		if c[1].URL == c[2].URL {
			t.Fatalf("install %d reads one part twice", i)
		}
		seen[c[1].URL], seen[c[2].URL] = true, true
	}
	if len(seen) != OwnBuckets {
		t.Fatalf("%d parts read, want %d", len(seen), OwnBuckets)
	}
	sealed, err := Seal(ownKey(), "vless://a\ntrojan://b\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := Open(string(sealed)); got != "vless://a\ntrojan://b\n" {
		t.Fatalf("opened %q", got)
	}
	OwnKey = strings.Repeat("cd", 32)
	if Open(string(sealed)) != "" {
		t.Fatal("opened with the wrong key")
	}
	if strings.Contains(string(sealed), "vless") {
		t.Fatal("sealed list readable")
	}
}

func TestMirrorsUseGitHubAPIOnlyForOwnRepo(t *testing.T) {
	defer func(old string) { WorkerMirror = old }(WorkerMirror)
	WorkerMirror = "https://accounts.example.org/, https://lists.example.workers.dev"
	own := Mirrors(CentralBase + "fresh.txt")
	if !slices.Contains(own, "https://api.github.com/repos/Albatrossvpn/Albatross/contents/fresh.txt?ref=servers") ||
		!slices.Contains(own, "https://accounts.example.org/list/servers/fresh.txt") ||
		!slices.Contains(own, "https://lists.example.workers.dev/list/servers/fresh.txt") {
		t.Fatalf("own repo: %v", own)
	}
	for _, m := range Mirrors("https://raw.githubusercontent.com/someone/lists/main/sub.txt") {
		if strings.Contains(m, "api.github.com") || strings.Contains(m, "example") {
			t.Fatalf("other repo uses the API: %s", m)
		}
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// The Worker shares the account service's name, so it is reached only
// through the VPN or the private client, never by a plain direct call.
func TestWorkerNotDirect(t *testing.T) {
	defer func(old string) { WorkerMirror = old }(WorkerMirror)
	WorkerMirror = "https://accounts.example.org"
	var mu sync.Mutex
	var direct, private []string
	record := func(to *[]string) *http.Client {
		return &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			mu.Lock()
			*to = append(*to, r.URL.String())
			mu.Unlock()
			return nil, errors.New("offline")
		})}
	}
	f := &Fetcher{Direct: record(&direct), CacheDir: t.TempDir()}
	src := Source{ID: "fresh", URL: CentralBase + "fresh.txt"}
	f.Fetch(context.Background(), src)
	f.Private = record(&private)
	f.Fetch(context.Background(), src)
	if len(direct) == 0 || slices.ContainsFunc(direct, IsWorker) {
		t.Fatalf("direct calls: %v", direct)
	}
	if len(private) != 1 || !IsWorker(private[0]) {
		t.Fatalf("private calls: %v", private)
	}
}

// The app's own files come from the project's own storage first, so they
// keep working once the repository is private; other files start at their
// own address.
func TestAddressesOwnStorageFirst(t *testing.T) {
	defer func(old string) { WorkerMirror = old }(WorkerMirror)
	WorkerMirror = "https://accounts.example.org,https://www.example.org/"
	got := Addresses(CentralBase + "hot.txt")
	want := []string{"https://accounts.example.org/list/servers/hot.txt", "https://www.example.org/list/servers/hot.txt", CentralBase + "hot.txt"}
	if len(got) < 4 || !slices.Equal(got[:3], want) {
		t.Fatalf("own file: %v", got)
	}
	other := "https://raw.githubusercontent.com/someone/lists/main/sub.txt"
	if got := Addresses(other); got[0] != other || slices.ContainsFunc(got, IsWorker) {
		t.Fatalf("other file: %v", got)
	}
	if got := Addresses("https://example.net/sub.txt"); !slices.Equal(got, []string{"https://example.net/sub.txt"}) {
		t.Fatalf("plain file: %v", got)
	}
}

func TestDomesticMirrors(t *testing.T) {
	defer func(a, b string) { WorkerMirror, DomesticMirror = a, b }(WorkerMirror, DomesticMirror)
	WorkerMirror, DomesticMirror = "https://accounts.example", "http://5.6.7.8:8081/tok, http://mirror.example.ir:8081/tok/"
	u := CentralBase + "own-0.bin"
	addrs := Addresses(u)
	if addrs[0] != "https://accounts.example/list/servers/own-0.bin" || addrs[1] != u {
		t.Fatalf("own storage then GitHub first: %v", addrs[:2])
	}
	last := addrs[len(addrs)-2:]
	if last[0] != "http://5.6.7.8:8081/tok/list/servers/own-0.bin" || last[1] != "http://mirror.example.ir:8081/tok/list/servers/own-0.bin" {
		t.Fatalf("domestic mirrors last as usual: %v", last)
	}
	if !IsDomestic(last[0]) || IsDomestic(u) || IsDomestic("http://5.6.7.8:8081/other") {
		t.Fatal("IsDomestic")
	}
	if len(Mirrors("https://raw.githubusercontent.com/other/repo/main/x.txt")) != 4 {
		t.Fatal("domestic mirrors serve only the app's own files")
	}
}

// Where foreign addresses are cut, the domestic mirrors come first and in
// their own order, the project's storage is still reached through the VPN,
// and foreign sites are not called directly, where they cannot answer.
func TestDomesticModeAttempts(t *testing.T) {
	defer func(a, b string) { WorkerMirror, DomesticMirror = a, b }(WorkerMirror, DomesticMirror)
	WorkerMirror, DomesticMirror = "https://accounts.example", "http://5.6.7.8:8081/tok,http://9.9.9.9:8081/tok"
	var mu sync.Mutex
	var direct, proxied []string
	record := func(to *[]string) *http.Client {
		return &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			mu.Lock()
			*to = append(*to, r.URL.String())
			mu.Unlock()
			return nil, errors.New("offline")
		})}
	}
	f := &Fetcher{Direct: record(&direct), Private: record(&direct), Mode: func() Mode { return ModeDomestic }, CacheDir: t.TempDir()}
	own := Source{ID: "own", URL: CentralBase + "own-0.bin"}
	f.Fetch(context.Background(), own)
	if len(direct) < 2 || !strings.HasPrefix(direct[0], "http://5.6.7.8:8081/") || !strings.HasPrefix(direct[1], "http://9.9.9.9:8081/") {
		t.Fatalf("disconnected: %v", direct)
	}
	f.Proxy = func() *http.Client { return record(&proxied) }
	direct = nil
	f.Fetch(context.Background(), own)
	if !slices.ContainsFunc(proxied, IsWorker) || slices.ContainsFunc(direct, IsWorker) {
		t.Fatalf("connected: through the VPN %v, direct %v", proxied, direct)
	}
	direct = nil
	for _, s := range []Source{
		{ID: "tg", Kind: KindTelegram, URL: "somechannel"},
		{ID: "leap", Kind: KindLEAP, URL: "https://api.black.riseup.net"},
		{ID: "feed", Kind: KindFeed, URL: "https://www.youtube.com/feeds/videos.xml?channel_id=x"},
	} {
		f.Fetch(context.Background(), s)
	}
	if len(direct) != 0 {
		t.Fatalf("foreign sites called directly: %v", direct)
	}
}

// A keyed list opens only with the key of its id; the collector's lists
// are offered keyed.
func TestKeyedLists(t *testing.T) {
	k := ListKey{ID: 1761480000, Key: []byte(strings.Repeat("k", 32))}
	sealed, err := SealKeyed(k, "vless://a@b:443#x\n")
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := KeyedID(string(sealed)); !ok || id != k.ID {
		t.Fatalf("id %d %v", id, ok)
	}
	if got := OpenKeyed([]ListKey{{ID: 1, Key: []byte(strings.Repeat("z", 32))}, k}, string(sealed)); got != "vless://a@b:443#x\n" {
		t.Fatalf("opened %q", got)
	}
	if OpenKeyed([]ListKey{{ID: k.ID, Key: []byte(strings.Repeat("z", 32))}}, string(sealed)) != "" {
		t.Fatal("opened with the wrong key")
	}
	if OpenKeyed([]ListKey{k}, "plain text") != "" || OpenKeyed(nil, string(sealed)) != "" {
		t.Fatal("opened without a key")
	}
	for _, s := range Central(3) {
		if s.Keyed != strings.HasSuffix(s.URL, KeyedSuffix) || (s.Hot && !s.Keyed) {
			t.Fatalf("keyed flag and name disagree: %+v", s)
		}
	}
}

func TestCollectedSourcesAreUnique(t *testing.T) {
	ids := map[string]string{}
	urls := map[string]string{}
	for _, s := range Collected() {
		if prev, dup := ids[s.ID]; dup {
			t.Errorf("%s %q is listed twice (%s)", s.Kind, s.URL, prev)
		}
		ids[s.ID] = s.URL
		if s.Kind == KindTelegram {
			continue
		}
		u := strings.ToLower(s.URL)
		if prev, dup := urls[u]; dup {
			t.Errorf("%q is listed as %s and %s", s.URL, prev, s.Kind)
		}
		urls[u] = string(s.Kind)
		if s.Kind == KindFeed || s.Kind == KindSubscription {
			if !strings.HasPrefix(u, "https://") {
				t.Errorf("%s %q is not an https address", s.Kind, s.URL)
			}
		}
	}
	feeds, dated := 0, 0
	for _, s := range Collected() {
		if s.Kind == KindFeed {
			feeds++
		}
		if strings.Contains(s.URL, "{YYYY}") {
			dated++
			if s.Kind != KindSubscription {
				t.Errorf("%s %q holds a date but is not a subscription", s.Kind, s.URL)
			}
		}
	}
	if feeds == 0 || dated == 0 {
		t.Fatalf("%d feeds and %d dated files, want some of each", feeds, dated)
	}
	// The apps read the built-in lists themselves: none of the collector-only ones may leak in.
	for _, s := range Builtin() {
		if s.Kind == KindFeed || strings.Contains(s.URL, "{YYYY}") {
			t.Errorf("the app would read %s %q", s.Kind, s.URL)
		}
	}
}
