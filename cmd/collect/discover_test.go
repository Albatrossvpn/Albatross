package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Albatrossvpn/vp/internal/sources"
)

func TestRotate(t *testing.T) {
	items := []string{"a", "b", "c", "d", "e"}
	if got := strings.Join(rotate(items, 0, 2), ""); got != "ab" {
		t.Fatalf("hour 0: %s", got)
	}
	if got := strings.Join(rotate(items, 1, 2), ""); got != "cd" {
		t.Fatalf("hour 1: %s", got)
	}
	if got := strings.Join(rotate(items, 2, 2), ""); got != "ea" {
		t.Fatalf("hour 2: %s", got)
	}
	// Every item comes up within a day.
	seen := map[string]bool{}
	for h := 0; h < 24; h++ {
		for _, q := range rotate(webQueries, h, webPerRun) {
			seen[q] = true
		}
	}
	if len(seen) != len(webQueries) {
		t.Fatalf("%d of %d queries searched in a day", len(seen), len(webQueries))
	}
}

func TestParseSearchPages(t *testing.T) {
	ddg := `<a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Ffree%2Fsub.txt&amp;rut=abc">x</a>
<a class="result__a" href="https://other.example.org/page">y</a>`
	got := parseDDG(ddg)
	if len(got) != 2 || got[0] != "https://example.com/free/sub.txt" || got[1] != "https://other.example.org/page" {
		t.Fatalf("ddg %v", got)
	}
	bing := `<li class="b_algo"><div><h2><a href="https://example.com/configs" h="ID">Free</a></h2></div></li>
<li class="b_algo" data-id iid=SERP.5337><div class="b_tpcn"><a class="tilk" href="https://www.bing.com/ck/a?!&amp;&amp;p=x&amp;u=a1aHR0cHM6Ly9jb25maWdzLmV4YW1wbGUub3JnL3N1Yg&amp;ntb=1">site</a></div><h2><a href="https://www.bing.com/ck/a?!&amp;&amp;p=x&amp;u=a1aHR0cHM6Ly9jb25maWdzLmV4YW1wbGUub3JnL3N1Yg&amp;ntb=1">Free</a></h2></li>
<li class="b_algo"><h2><a href="https://www.bing.com/x">no</a></h2></li>`
	if got := parseBing(bing); len(got) != 2 || got[0] != "https://example.com/configs" || got[1] != "https://configs.example.org/sub" {
		t.Fatalf("bing %v", got)
	}
}

func TestCanonicalPage(t *testing.T) {
	if canonicalPage("http://example.com/?utm=1#x") != "https://example.com/" {
		t.Fatal("front page kept")
	}
	if canonicalPage("https://www.youtube.com/watch?v=1") != "" || canonicalPage("ftp://x/") != "" {
		t.Fatal("skipped hosts and schemes")
	}
	if canonicalPage("https://github.com/o/r/blob/main/sub.txt") != "https://raw.githubusercontent.com/o/r/main/sub.txt" {
		t.Fatal("blob pages still become raw")
	}
	if !listLike("a/b/sub.txt") || !listLike("configs") || listLike("x/image.png") || listLike(".gitignore") || listLike(".github/ISSUE_TEMPLATE/bug.yml") {
		t.Fatal("listLike")
	}
}

func TestCarryPrevious(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/sources.txt", []byte("# found\nhttps://a.example/sub.txt\nhttps://b.example/sub.txt\n"), 0o644)
	os.WriteFile(dir+"/channels.txt", []byte("ChanOne\n"), 0o644)
	os.WriteFile(dir+"/discover.json", []byte(`{"misses":{"https://b.example/sub.txt":23}}`), 0o644)
	d := newDiscoverer("", nil)
	subs, tgs := d.loadPrev(dir)
	if len(subs) != 2 || len(tgs) != 1 || !d.prev["chanone"] || d.misses["https://b.example/sub.txt"] != 23 {
		t.Fatalf("loadPrev %v %v %v", subs, tgs, d.misses)
	}
	sub := func(u string) sources.Source { return sources.Source{Kind: sources.KindSubscription, URL: u} }
	if !d.carry(sub("https://a.example/sub.txt"), true) || d.misses["https://a.example/sub.txt"] != 1 {
		t.Fatal("one miss is carried")
	}
	if d.carry(sub("https://b.example/sub.txt"), true) {
		t.Fatal("the 24th miss drops the source")
	}
	if d.carry(sub("https://new.example/sub.txt"), true) {
		t.Fatal("a source that was never published is not carried")
	}
	if !d.carry(sources.Source{Kind: sources.KindTelegram, URL: "ChanOne"}, false) || d.channels[0] != "chanone" {
		t.Fatal("unchecked channel carried, lower-cased")
	}
	if len(d.subs) != 1 || d.stats["dropped"] != 1 || d.stats["carried_missing"] != 1 || d.stats["carried_unchecked"] != 1 {
		t.Fatalf("subs %v stats %v", d.subs, d.stats)
	}
	if got := uniq([]string{"b", "a", "b"}); len(got) != 2 || got[0] != "a" {
		t.Fatalf("uniq %v", got)
	}
}

// TestWebSearchLive asks the real engines (DISCOVER_LIVE=1).
func TestWebSearchLive(t *testing.T) {
	if os.Getenv("DISCOVER_LIVE") == "" {
		t.Skip("DISCOVER_LIVE not set")
	}
	d := newDiscoverer("", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for name, fn := range map[string]func(context.Context, string) ([]string, error){
		"ddg": d.searchDDG, "bing": d.searchBing, "marginalia": d.searchMarginalia, "reddit": d.searchReddit,
		"gitlab": d.gitlabFiles, "codeberg": d.codebergFiles,
	} {
		q := "free v2ray subscription"
		if name == "gitlab" || name == "codeberg" {
			q = "v2ray"
		}
		res, err := fn(ctx, q)
		t.Logf("%s: %d results, err %v; first: %v", name, len(res), err, res[:min(3, len(res))])
	}
}

// A skipped site is the site or a subdomain of it, never a name that
// merely ends the same way.
func TestSkipHost(t *testing.T) {
	for _, h := range []string{"x.com", "mobile.x.com", "www.youtube.com", "google.com", "www.google.de", "play.google.com", "amazon.co.uk"} {
		if !skipHost(h) {
			t.Errorf("%s not skipped", h)
		}
	}
	for _, h := range []string{"v2rayx.com", "clashx.com", "freebing.com", "mygoogle.com", "googleusercontent.com", "raw.githubusercontent.com"} {
		if skipHost(h) {
			t.Errorf("%s skipped", h)
		}
	}
}

type fakeTransport map[string]string

func (f fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	body, ok := f[r.URL.String()]
	code := http.StatusOK
	if !ok {
		code = http.StatusNotFound
	}
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: r}, nil
}

// An empty project among the results (no default branch) is passed over;
// the projects after it are still listed.
func TestCodeHostsSkipEmptyProjects(t *testing.T) {
	d := newDiscoverer("", nil)
	d.client = &http.Client{Transport: fakeTransport{
		"https://gitlab.com/api/v4/projects?order_by=last_activity_at&per_page=50&search=v2ray": `[{"id":1,"path_with_namespace":"a/empty","default_branch":null},{"id":2,"path_with_namespace":"b/lists","default_branch":"main"}]`,
		"https://gitlab.com/api/v4/projects/2/repository/tree?recursive=true&per_page=100":      `[{"path":"sub.txt","type":"blob"}]`,
		"https://codeberg.org/api/v1/repos/search?sort=updated&order=desc&limit=50&q=v2ray":     `{"data":[{"full_name":"a/empty","default_branch":""},{"full_name":"b/lists","default_branch":"main"}]}`,
		"https://codeberg.org/api/v1/repos/b/lists/git/trees/main?recursive=true&per_page=1000": `{"tree":[{"path":"sub.txt","type":"blob"}]}`,
	}}
	gl, err := d.gitlabFiles(context.Background(), "v2ray")
	if err != nil || len(gl) != 1 || gl[0] != "https://gitlab.com/b/lists/-/raw/main/sub.txt" {
		t.Fatalf("gitlab: %v %v", gl, err)
	}
	cb, err := d.codebergFiles(context.Background(), "v2ray")
	if err != nil || len(cb) != 1 || cb[0] != "https://codeberg.org/b/lists/raw/branch/main/sub.txt" {
		t.Fatalf("codeberg: %v %v", cb, err)
	}
}

// What the run did not get to is carried as it was, except a source the
// shipped lists now hold, and one that can no longer be a candidate, which
// counts a miss.
func TestCarryRest(t *testing.T) {
	d := newDiscoverer("", nil)
	for _, k := range []string{"https://a.example/sub.txt", "https://shipped.example/sub.txt", "https://www.youtube.com/sub.txt", "chanone", "shippedchan"} {
		d.prev[k] = true
	}
	known := map[string]bool{"https://shipped.example/sub.txt": true, "shippedchan": true}
	d.carryRest([]string{"https://a.example/sub.txt", "https://shipped.example/sub.txt", "https://www.youtube.com/sub.txt"}, []string{"ChanOne", "ShippedChan"}, known)
	if len(d.subs) != 2 || d.subs[0] != "https://a.example/sub.txt" || len(d.channels) != 1 || d.channels[0] != "chanone" {
		t.Fatalf("subs %v channels %v", d.subs, d.channels)
	}
	if d.misses["https://www.youtube.com/sub.txt"] != 1 || d.misses["https://a.example/sub.txt"] != 0 {
		t.Fatalf("misses %v", d.misses)
	}
}

// The subscriptions inside apps' import links are sources too, their
// tokens kept.
func TestImportLinksMentioned(t *testing.T) {
	text := "happ://add/https://sub.example.com/s/abc123 " +
		"v2rayng://install-sub?url=https%3A%2F%2Fpanel.example.org%2Fapi%2Fsub%3Ftoken%3Dxyz&name=free " +
		"sing-box://import-remote-profile?url=https%3A%2F%2Fa.example.net%2Fc.json#n " +
		"happ://add/https://example.com/ happ://crypt5/abcdef"
	subs, _ := mentions(text)
	want := map[string]bool{"https://sub.example.com/s/abc123": true, "https://panel.example.org/api/sub?token=xyz": true, "https://a.example.net/c.json": true}
	for _, s := range subs {
		if !want[s] {
			t.Errorf("unexpected %s", s)
		}
		delete(want, s)
	}
	if len(want) > 0 {
		t.Errorf("missing %v from %v", want, subs)
	}
}

// The newest public gists that may hold servers are read, by description
// or file name; forks pushed to lately are read at their original's paths;
// lists on paste sites are mentions too.
func TestGistsForksAndPastes(t *testing.T) {
	d := newDiscoverer("t", nil)
	recent := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	old := time.Now().UTC().AddDate(0, -2, 0).Format(time.RFC3339)
	d.client = &http.Client{Transport: fakeTransport{
		"https://api.github.com/gists/public?per_page=100&page=1": `[
			{"description":"my vless configs","files":{"a.txt":{"raw_url":"https://gist.githubusercontent.com/u/1/raw/a.txt","size":900}}},
			{"description":"shopping list","files":{"b.md":{"raw_url":"https://gist.githubusercontent.com/u/2/raw/b.md","size":900}}},
			{"description":"","files":{"clash.yaml":{"raw_url":"https://gist.githubusercontent.com/u/3/raw/clash.yaml","size":5000}}},
			{"description":"v2ray","files":{"tiny.txt":{"raw_url":"https://gist.githubusercontent.com/u/4/raw/tiny.txt","size":10}}}]`,
		"https://api.github.com/repos/owner/collector/forks?sort=newest&per_page=30": `[{"full_name":"fresh/collector","pushed_at":"` + recent + `"},{"full_name":"stale/collector","pushed_at":"` + old + `"}]`,
	}}
	gists := d.githubGists(context.Background())
	if len(gists) != 2 || gists[0] != "https://gist.githubusercontent.com/u/1/raw/a.txt" && gists[1] != "https://gist.githubusercontent.com/u/1/raw/a.txt" {
		t.Fatalf("gists: %v", gists)
	}
	forks := d.githubForks(context.Background(), []string{"https://raw.githubusercontent.com/owner/collector/main/sub/all.txt", "https://example.com/x.txt"}, 0)
	if len(forks) != 1 || forks[0] != "https://raw.githubusercontent.com/fresh/collector/HEAD/sub/all.txt" {
		t.Fatalf("forks: %v", forks)
	}
	subs, _ := mentions("get it at https://pastebin.com/raw/AbC123xy or https://rentry.co/freev2/raw, and https://gitee.com/a/b/raw/master/sub.txt")
	if len(subs) < 3 {
		t.Fatalf("paste sites: %v", subs)
	}
}
