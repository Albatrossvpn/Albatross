package main

import (
	"context"
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
