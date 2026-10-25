package main

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Albatrossvpn/vp/internal/node"
	"github.com/Albatrossvpn/vp/internal/sources"
)

// Source discovery (-discover): the shipped lists are a few hundred
// subscription files and a few hundred Telegram channels; this finds many
// more and keeps the ones that carry servers, so the collector draws from
// several times as many sources and from sources for every protocol.
//
// Where they come from:
//   - GitHub code search for every link scheme ("vless://", "hysteria2://",
//     "tuic://" ... and OpenVPN profiles), and repository search for the
//     usual words, whose files are listed through the Git tree API;
//   - every address and channel mentioned inside the lists and channels
//     already known (aggregators list their own sources), and the channels
//     those channels mention, two steps out;
//   - each candidate is fetched and kept only when it holds at least a few
//     servers; sources with the very same contents are kept once.
//
// Output: sources.txt (subscription addresses), channels.txt (Telegram
// channels) and discover.json (counts), which the collector takes with
// -sources and -channels.

// Schemes the GitHub code search looks for, one query each.
var discoverSchemes = []string{
	"vless", "vmess", "trojan", "ss", "ssr", "hysteria2", "hy2", "hysteria", "tuic", "anytls",
	"wireguard", "wg", "socks5", "socks", "ssh", "naive+https", "warp",
}

// Repository searches: the words the usual lists are described with.
var discoverRepoQueries = []string{
	"v2ray subscription free", "free vpn config", "hysteria2 free", "vless reality free",
	"proxy config collector", "sub store v2ray", "free proxies iran", "تنظیمات v2ray",
}

// minSourceLinks is how many servers a subscription must hold to count,
// and minChannelLinks how many a channel's recent posts must.
const minSourceLinks, minChannelLinks = 5, 3

// Caps that keep a run within the hour.
const maxCandidates, maxChannelPages = 20000, 6000

var (
	// Mentions of channels: t.me/name, @name.
	tgMentionRe = regexp.MustCompile(`(?i)(?:t\.me/(?:s/)?|@)([A-Za-z][A-Za-z0-9_]{4,31})\b`)
	// Addresses of lists inside texts: GitHub raw or blob files and other
	// https files of the usual types.
	listURLRe = regexp.MustCompile(`https?://(?:raw\.githubusercontent\.com|github\.com|gist\.githubusercontent\.com|cdn\.jsdelivr\.net|[a-z0-9.-]+\.(?:workers\.dev|pages\.dev|github\.io|vercel\.app|netlify\.app|dpdns\.org|xyz|top|ir|ru|net|org|com))/[^\s"'<>()\[\]]+\.(?:txt|yaml|yml|json|conf|ovpn)\b[^\s"'<>()\[\]]*`)
	blobRe    = regexp.MustCompile(`^https?://github\.com/([^/]+)/([^/]+)/(?:blob|raw)/([^/]+)/(.+)$`)
)

// Telegram names that are pages or words, never channels.
var tgNotChannel = map[string]bool{
	"joinchat": true, "share": true, "proxy": true, "socks": true, "addstickers": true, "iv": true, "login": true,
	"setlanguage": true, "confirmphone": true, "contact": true, "addlist": true, "boost": true, "giftcode": true,
	"channel": true, "telegram": true, "username": true, "admin": true, "support": true, "everyone": true,
}

type discoverer struct {
	token  string
	client *http.Client
	f      *sources.Fetcher

	mu       sync.Mutex
	seenSub  map[string]bool
	seenTG   map[string]bool
	contents map[uint64]string // hash of a kept source's text -> its address
	subs     []string
	channels []string
	stats    map[string]int
}

func newDiscoverer(token string, f *sources.Fetcher) *discoverer {
	return &discoverer{
		token: token, client: &http.Client{Timeout: 60 * time.Second}, f: f,
		seenSub: map[string]bool{}, seenTG: map[string]bool{}, contents: map[uint64]string{}, stats: map[string]int{},
	}
}

// runDiscover finds sources and writes them to dir.
func runDiscover(ctx context.Context, dir, token string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}
	cache, err := os.MkdirTemp("", "discover")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(cache)
	d := newDiscoverer(token, &sources.Fetcher{Direct: &http.Client{Timeout: 90 * time.Second}, CacheDir: cache})

	// What is known already: never reported again, but read for mentions.
	var knownSubs, knownTG []string
	for _, s := range sources.Collected() {
		switch s.Kind {
		case sources.KindSubscription:
			d.seenSub[canonical(s.URL)] = true
			knownSubs = append(knownSubs, s.URL)
		case sources.KindTelegram:
			d.seenTG[strings.ToLower(s.URL)] = true
			knownTG = append(knownTG, strings.ToLower(s.URL))
		}
	}
	log.Printf("discover: %d subscriptions and %d channels known", len(knownSubs), len(knownTG))

	var candSubs []string
	var candTG []string
	add := func(subs, tgs []string) {
		d.mu.Lock()
		defer d.mu.Unlock()
		for _, u := range subs {
			if c := canonical(u); c != "" && !d.seenSub[c] {
				d.seenSub[c] = true
				candSubs = append(candSubs, c)
			}
		}
		for _, c := range tgs {
			if c = strings.ToLower(c); !d.seenTG[c] && !tgNotChannel[c] {
				d.seenTG[c] = true
				candTG = append(candTG, c)
			}
		}
	}

	// 1. Mentions inside the known lists and channels.
	texts := d.fetchAll(ctx, knownSubs, knownTG)
	for _, t := range texts {
		s, c := mentions(t)
		add(s, c)
	}
	d.stats["from_known"] = len(candSubs) + len(candTG)
	log.Printf("discover: %d addresses and %d channels mentioned in known sources", len(candSubs), len(candTG))

	// 2. GitHub: code search per scheme, repository search by words.
	if token != "" {
		gh := d.githubCode(ctx)
		gh = append(gh, d.githubRepos(ctx)...)
		before := len(candSubs)
		add(gh, nil)
		d.stats["from_github"] = len(candSubs) - before
		log.Printf("discover: %d new addresses from GitHub", len(candSubs)-before)
	} else {
		log.Print("discover: no GITHUB_TOKEN; GitHub search skipped")
	}

	// 3. Fetch the candidates; keep the ones with servers; read their
	// mentions too (one more step), then the channels' mentions once more.
	for step := 0; step < 2 && (len(candSubs) > 0 || len(candTG) > 0); step++ {
		subs, tgs := candSubs, candTG
		if len(subs) > maxCandidates {
			subs = subs[:maxCandidates]
		}
		if len(tgs) > maxChannelPages {
			tgs = tgs[:maxChannelPages]
		}
		candSubs, candTG = nil, nil
		log.Printf("discover: step %d: trying %d addresses and %d channels", step+1, len(subs), len(tgs))
		kept := d.keep(ctx, subs, tgs)
		var ns, nc []string
		for _, t := range kept {
			s, c := mentions(t)
			ns, nc = append(ns, s...), append(nc, c...)
		}
		add(ns, nc)
	}

	sort.Strings(d.subs)
	sort.Strings(d.channels)
	write := func(name string, lines []string, comment string) {
		b := comment + "\n" + strings.Join(lines, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(b), 0o644); err != nil {
			log.Fatal(err)
		}
	}
	write("sources.txt", d.subs, "# Subscription addresses found by cmd/collect -discover; each held servers when found.")
	write("channels.txt", d.channels, "# Telegram channels found by cmd/collect -discover; each posted servers when found.")
	d.stats["sources"], d.stats["channels"] = len(d.subs), len(d.channels)
	meta := map[string]any{"updated": time.Now().UTC(), "stats": d.stats}
	b, _ := json.MarshalIndent(meta, "", "  ")
	os.WriteFile(filepath.Join(dir, "discover.json"), append(b, '\n'), 0o644)
	log.Printf("discover: kept %d subscriptions and %d channels", len(d.subs), len(d.channels))
}

// fetchAll fetches sources and channels and returns their texts.
func (d *discoverer) fetchAll(ctx context.Context, subs, tgs []string) []string {
	var out []string
	var mu sync.Mutex
	sem := make(chan struct{}, 24)
	var wg sync.WaitGroup
	get := func(s sources.Source) {
		defer func() { <-sem; wg.Done() }()
		res := d.f.Fetch(ctx, s)
		if res.Err == nil && res.Text != "" {
			mu.Lock()
			out = append(out, res.Text)
			mu.Unlock()
		}
	}
	for _, u := range subs {
		sem <- struct{}{}
		wg.Add(1)
		go get(sources.Source{ID: sources.ID(sources.KindSubscription, u), Kind: sources.KindSubscription, URL: u})
	}
	for _, c := range tgs {
		sem <- struct{}{}
		wg.Add(1)
		go get(sources.Source{ID: sources.ID(sources.KindTelegram, c), Kind: sources.KindTelegram, URL: c})
	}
	wg.Wait()
	return out
}

// keep fetches candidates and keeps those with servers; it returns the
// texts of the kept ones.
func (d *discoverer) keep(ctx context.Context, subs, tgs []string) []string {
	var texts []string
	var mu sync.Mutex
	sem := make(chan struct{}, 24)
	var wg sync.WaitGroup
	try := func(s sources.Source, min int) {
		defer func() { <-sem; wg.Done() }()
		res := d.f.Fetch(ctx, s)
		if res.Err != nil || res.Text == "" {
			return
		}
		n := 0
		node.EachText(res.Text, func(*node.Node) { n++ })
		if n < min {
			return
		}
		h := fnv.New64a()
		io.WriteString(h, res.Text)
		sum := h.Sum64()
		d.mu.Lock()
		defer d.mu.Unlock()
		if s.Kind == sources.KindSubscription {
			if other, dup := d.contents[sum]; dup {
				d.stats["duplicates"]++
				_ = other
				return
			}
			d.contents[sum] = s.URL
			d.subs = append(d.subs, s.URL)
		} else {
			d.channels = append(d.channels, s.URL)
		}
		mu.Lock()
		texts = append(texts, res.Text)
		mu.Unlock()
	}
	for _, u := range subs {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go try(sources.Source{ID: sources.ID(sources.KindSubscription, u), Kind: sources.KindSubscription, URL: u}, minSourceLinks)
	}
	for _, c := range tgs {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go try(sources.Source{ID: sources.ID(sources.KindTelegram, c), Kind: sources.KindTelegram, URL: c}, minChannelLinks)
	}
	wg.Wait()
	return texts
}

// mentions finds list addresses and channel names in a text.
func mentions(text string) (subs, channels []string) {
	for _, m := range listURLRe.FindAllString(text, -1) {
		if u := canonical(m); u != "" {
			subs = append(subs, u)
		}
	}
	for _, m := range tgMentionRe.FindAllStringSubmatch(text, -1) {
		channels = append(channels, strings.ToLower(m[1]))
	}
	return subs, channels
}

// canonical turns a list address into the form it is kept in: GitHub blob
// pages become raw addresses on the default branch, query strings and
// fragments go, and anything that is not an https file is dropped.
func canonical(u string) string {
	u = strings.TrimSpace(u)
	u = strings.TrimRight(u, ".,;:)'\"")
	if m := blobRe.FindStringSubmatch(u); m != nil {
		u = "https://raw.githubusercontent.com/" + m[1] + "/" + m[2] + "/" + m[3] + "/" + m[4]
	}
	p, err := url.Parse(u)
	if err != nil || (p.Scheme != "https" && p.Scheme != "http") || p.Host == "" || p.Path == "" || p.Path == "/" {
		return ""
	}
	p.Scheme = "https"
	p.Fragment = ""
	if p.Host != "raw.githubusercontent.com" {
		// Query strings often carry a token; GitHub raw ones never need one.
		p.RawQuery = ""
	} else {
		p.RawQuery = ""
		// Pinned to a commit: the branch keeps moving, the commit does not.
		parts := strings.SplitN(strings.TrimPrefix(p.Path, "/"), "/", 4)
		if len(parts) == 4 && len(parts[2]) == 40 && isHex(parts[2]) {
			p.Path = "/" + parts[0] + "/" + parts[1] + "/HEAD/" + parts[3]
		}
	}
	return p.String()
}

func isHex(s string) bool {
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return s != ""
}

// githubCode searches GitHub's code for files holding each scheme and
// OpenVPN profiles, and returns their raw addresses on the default branch.
func (d *discoverer) githubCode(ctx context.Context) []string {
	var out []string
	queries := make([]string, 0, len(discoverSchemes)+2)
	for _, s := range discoverSchemes {
		queries = append(queries, fmt.Sprintf(`"%s://"`, s))
	}
	queries = append(queries, `path:*.ovpn "remote "`, `"proxies:" "server:" path:*.yaml`)
	for _, q := range queries {
		n := 0
		for page := 1; page <= 10; page++ {
			var res struct {
				Items []struct {
					Path       string `json:"path"`
					Repository struct {
						FullName string `json:"full_name"`
					} `json:"repository"`
				} `json:"items"`
			}
			u := "https://api.github.com/search/code?per_page=100&page=" + strconv.Itoa(page) + "&q=" + url.QueryEscape(q)
			if err := d.github(ctx, u, &res); err != nil {
				log.Printf("discover: code search %s page %d: %v", q, page, err)
				break
			}
			for _, it := range res.Items {
				out = append(out, "https://raw.githubusercontent.com/"+it.Repository.FullName+"/HEAD/"+it.Path)
				n++
			}
			if len(res.Items) < 100 {
				break
			}
		}
		log.Printf("discover: code search %s: %d files", q, n)
	}
	return out
}

// githubRepos searches repositories by the usual words and lists the
// files of each that look like server lists.
func (d *discoverer) githubRepos(ctx context.Context) []string {
	repos := map[string]bool{}
	for _, q := range discoverRepoQueries {
		var res struct {
			Items []struct {
				FullName string `json:"full_name"`
			} `json:"items"`
		}
		u := "https://api.github.com/search/repositories?per_page=100&sort=updated&q=" + url.QueryEscape(q)
		if err := d.github(ctx, u, &res); err != nil {
			log.Printf("discover: repository search %s: %v", q, err)
			continue
		}
		for _, it := range res.Items {
			repos[it.FullName] = true
		}
	}
	var out []string
	n := 0
	for repo := range repos {
		if n >= 400 {
			break
		}
		n++
		var tree struct {
			Tree []struct {
				Path string `json:"path"`
				Type string `json:"type"`
				Size int    `json:"size"`
			} `json:"tree"`
		}
		if err := d.github(ctx, "https://api.github.com/repos/"+repo+"/git/trees/HEAD?recursive=1", &tree); err != nil {
			continue
		}
		for _, t := range tree.Tree {
			if t.Type != "blob" || t.Size < 64 || t.Size > 20<<20 {
				continue
			}
			ext := strings.ToLower(filepath.Ext(t.Path))
			switch ext {
			case ".txt", ".yaml", ".yml", ".json", ".conf", ".ovpn", "":
				out = append(out, "https://raw.githubusercontent.com/"+repo+"/HEAD/"+t.Path)
			}
		}
	}
	log.Printf("discover: %d repositories listed, %d files", n, len(out))
	return out
}

// github calls the API, waiting out its rate limits.
func (d *discoverer) github(ctx context.Context, u string, out any) error {
	for try := 0; try < 6; try++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("Authorization", "Bearer "+d.token)
		resp, err := d.client.Do(req)
		if err != nil {
			return err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusOK:
			return json.Unmarshal(body, out)
		case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
			wait := 65 * time.Second
			if ra, _ := strconv.Atoi(resp.Header.Get("Retry-After")); ra > 0 {
				wait = time.Duration(ra) * time.Second
			} else if reset, _ := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); reset > 0 {
				wait = time.Until(time.Unix(reset, 0)) + time.Second
			}
			if wait > 3*time.Minute {
				return fmt.Errorf("rate limited for %s", wait.Round(time.Second))
			}
			select {
			case <-time.After(max(wait, 5*time.Second)):
			case <-ctx.Done():
				return ctx.Err()
			}
		case resp.StatusCode == http.StatusUnprocessableEntity:
			return fmt.Errorf("HTTP 422: %s", strings.TrimSpace(string(body)))
		default:
			return fmt.Errorf("HTTP %d", resp.StatusCode)
		}
	}
	return fmt.Errorf("gave up on %s", u)
}

// extraSources reads addresses or channel names, one per line, from a
// discovered list (sources.txt, channels.txt); a missing file is empty.
func extraSources(path string, kind sources.Kind) []sources.Source {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		log.Printf("sources: %v", err)
		return nil
	}
	var out []sources.Source
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		out = append(out, sources.Source{ID: sources.ID(kind, l), Kind: kind, URL: l, Tier: sources.TierDeep, Enabled: true})
	}
	return out
}
