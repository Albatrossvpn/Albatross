package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Albatrossvpn/vp/internal/node"
)

// The web beyond GitHub and Telegram: sites that give away configurations,
// blogs that list sources, forums, other code hosts. Searched through
// engines that answer without a key, each run with a slice of the queries
// (webQueries, rotated by the hour), so the hourly runs between them cover
// everything within a few hours without hammering any one engine. Every
// result page is read: a page with servers on it becomes a source itself,
// and the lists and channels it mentions become candidates.

// webQueries are what people search for when they look for free servers,
// in the languages the sources are written in.
var webQueries = []string{
	`"vless://" free`, `"vmess://" subscription`, `"trojan://" free config`, `"ss://" free shadowsocks`,
	`"hysteria2://" free`, `"hy2://" config`, `"tuic://" free`, `"anytls://"`, `"wireguard" free config txt`,
	`free openvpn ovpn profiles download`, `free v2ray subscription link`, `free v2ray sub.txt`,
	`free clash subscription yaml`, `free sing-box subscription json`, `free proxy configs daily update`,
	`v2ray config free iran`, `vless reality free server`, `free hysteria server list`,
	`کانفیگ رایگان v2ray`, `سرور رایگان vless`, `اشتراک رایگان v2ray`, `کانفیگ رایگان هیستریا`, `کانفیگ رایگان تروجان`,
	`کانفیگ رایگان وایرگارد`, `لینک ساب رایگان v2ray`, `فیلترشکن رایگان کانفیگ`,
	`бесплатные конфиги v2ray`, `бесплатный vless сервер`, `бесплатная подписка v2ray`, `бесплатный shadowsocks`,
	`免费节点 v2ray 订阅`, `免费 vless 节点`, `免费 clash 订阅 每日更新`, `免费 hysteria2 节点`, `免费 trojan 节点`,
	`free v2ray telegram channel`, `v2ray configs github.io free`, `free proxies workers.dev vless`,
	`free ssr subscription`, `free socks5 proxy list vless`, `naive proxy free config`,
	// site: pulls the channels, repositories and hosts where lists live
	// straight out of every engine; readPages then reads each one.
	`site:t.me vless vmess config`, `site:t.me اشتراک v2ray`, `site:t.me 免费 节点`, `site:t.me free config`,
	`site:t.me hysteria2 tuic`, `site:t.me reality vless`,
	`site:github.io vless vmess sub`, `site:github.com v2ray subscription`, `site:gist.github.com vmess vless`,
	`"pages.dev" vless vmess sub`, `"workers.dev" vless subscription`, `"vercel.app" v2ray sub`,
	`site:gitlab.com v2ray subscription`, `site:gitee.com v2ray 订阅`, `site:rentry.co vless vmess`,
	// Import links point straight at a live subscription endpoint; mentions
	// pulls the address out of them (importRe). People paste them on the web
	// so a tap adds the whole list to an app.
	`"happ://add/" v2ray`, `"hiddify://import/" subscription`, `"clash://install-config" url`,
	// More high-signal phrasings and the newest protocols, in each language.
	`free vless reality vision xtls subscription`, `site:github.io free vless reality sub`,
	`免费 机场 订阅 链接 每日更新`, `بهترین کانفیگ رایگان v2ray روزانه تلگرام`, `бесплатная подписка vless reality`,
}

// webPerRun is how many queries each engine gets per run.
const webPerRun = 8

// rotate returns the slice of items an hourly run takes: webPerRun of
// them, moving forward each hour so every item comes up within a day.
func rotate(items []string, hour int, per int) []string {
	if len(items) == 0 {
		return nil
	}
	start := (hour * per) % len(items)
	var out []string
	for i := 0; i < per && i < len(items); i++ {
		out = append(out, items[(start+i)%len(items)])
	}
	return out
}

const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"

// webGet fetches a page as a browser would and returns its body.
func (d *discoverer) webGet(ctx context.Context, u string, limit int64) ([]byte, int, error) {
	return d.webGetAs(ctx, u, limit, "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.8", browserUA)
}

// apiGet fetches JSON from an API as the program it is: code hosts turn
// away a browser's name that asks for JSON ("old Chrome version") and
// challenge requests that ask for HTML.
func (d *discoverer) apiGet(ctx context.Context, u string, limit int64) ([]byte, int, error) {
	return d.webGetAs(ctx, u, limit, "application/json", "albatross-collect/1.0 (source discovery)")
}

func (d *discoverer) webGetAs(ctx context.Context, u string, limit int64, accept, ua string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Language", "en,fa;q=0.8,ru;q=0.6,zh;q=0.5")
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	return b, resp.StatusCode, err
}

var (
	ddgResultRe  = regexp.MustCompile(`class="result__a"[^>]*href="([^"]+)"`)
	bingResultRe = regexp.MustCompile(`<li class="b_algo"[^>]*>.*?<h2[^>]*>\s*<a[^>]*href="(https?://[^"]+)"`)
	// Bing's results are redirect links; the address is base64 in u=a1….
	bingRedirectRe = regexp.MustCompile(`[?&]u=a1([A-Za-z0-9_-]+)`)
	textURLRe      = regexp.MustCompile(`https?://[^\s)\]"'<>]+`)
)

// searchDDG returns the result addresses of a DuckDuckGo search (the HTML
// version, which needs no key).
func (d *discoverer) searchDDG(ctx context.Context, q string) ([]string, error) {
	b, status, err := d.webGet(ctx, "https://html.duckduckgo.com/html/?q="+url.QueryEscape(q), 2<<20)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", status)
	}
	return parseDDG(string(b)), nil
}

// parseDDG reads the result links out of DuckDuckGo's HTML: they are
// redirect links whose uddg parameter holds the address.
func parseDDG(page string) []string {
	var out []string
	for _, m := range ddgResultRe.FindAllStringSubmatch(page, -1) {
		h := html.UnescapeString(m[1])
		if strings.HasPrefix(h, "//") {
			h = "https:" + h
		}
		p, err := url.Parse(h)
		if err != nil {
			continue
		}
		if t := p.Query().Get("uddg"); t != "" {
			h = t
		}
		if strings.HasPrefix(h, "http") && !strings.Contains(h, "duckduckgo.com") {
			out = append(out, h)
		}
	}
	return out
}

// searchBing returns the result addresses of a Bing search page.
func (d *discoverer) searchBing(ctx context.Context, q string) ([]string, error) {
	b, status, err := d.webGet(ctx, "https://www.bing.com/search?count=30&q="+url.QueryEscape(q), 2<<20)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", status)
	}
	return parseBing(string(b)), nil
}

func parseBing(page string) []string {
	var out []string
	for _, m := range bingResultRe.FindAllStringSubmatch(strings.ReplaceAll(page, "\n", " "), -1) {
		h := html.UnescapeString(m[1])
		if r := bingRedirectRe.FindStringSubmatch(h); r != nil {
			b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(r[1], "="))
			if err != nil {
				continue
			}
			h = string(b)
		}
		if strings.HasPrefix(h, "http") && !strings.Contains(h, "bing.com") && !strings.Contains(h, "microsoft.com") {
			out = append(out, h)
		}
	}
	return out
}

// searchMarginalia uses the Marginalia search engine's public API, which
// favours small independent sites, where free configurations often live.
func (d *discoverer) searchMarginalia(ctx context.Context, q string) ([]string, error) {
	b, status, err := d.apiGet(ctx, "https://api.marginalia.nu/public/search/"+url.PathEscape(q)+"?count=20", 2<<20)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", status)
	}
	var res struct {
		Results []struct {
			URL string `json:"url"`
		} `json:"results"`
	}
	if err := json.Unmarshal(b, &res); err != nil {
		return nil, err
	}
	var out []string
	for _, r := range res.Results {
		out = append(out, r.URL)
	}
	return out, nil
}

// searchReddit reads Reddit's search as JSON: posts that share servers,
// and the links in them.
func (d *discoverer) searchReddit(ctx context.Context, q string) ([]string, error) {
	b, status, err := d.apiGet(ctx, "https://www.reddit.com/search.json?sort=new&limit=100&q="+url.QueryEscape(q), 4<<20)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", status)
	}
	var res struct {
		Data struct {
			Children []struct {
				Data struct {
					URL       string `json:"url"`
					Selftext  string `json:"selftext"`
					Permalink string `json:"permalink"`
				} `json:"data"`
			} `json:"children"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &res); err != nil {
		return nil, err
	}
	var out []string
	for _, c := range res.Data.Children {
		if u := c.Data.URL; strings.HasPrefix(u, "http") && !strings.Contains(u, "reddit.com") {
			out = append(out, u)
		}
		if c.Data.Permalink != "" {
			out = append(out, "https://www.reddit.com"+c.Data.Permalink)
		}
		out = append(out, textURLRe.FindAllString(html.UnescapeString(c.Data.Selftext), -1)...)
	}
	return out, nil
}

// searxInstances are public SearXNG servers that answer JSON. SearXNG asks
// many engines at once (Google, Bing, Yandex, Brave, Qwant and more) and
// hands back one list, so one query reaches what no single engine would.
// Each run takes the next instance, so no one of them is leaned on.
var searxInstances = []string{
	"https://searx.be", "https://baresearch.org", "https://search.inetol.net",
	"https://priv.au", "https://opnxng.com", "https://search.rhscz.eu",
	"https://searx.tiekoetter.com", "https://search.hbubli.cc",
}

var searxNext atomic.Int64

// searchSearx queries a SearXNG instance for JSON results.
func (d *discoverer) searchSearx(ctx context.Context, q string) ([]string, error) {
	base := searxInstances[int(searxNext.Add(1))%len(searxInstances)]
	u := base + "/search?format=json&safesearch=0&q=" + url.QueryEscape(q)
	b, status, err := d.apiGet(ctx, u, 4<<20)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", status)
	}
	var res struct {
		Results []struct {
			URL string `json:"url"`
		} `json:"results"`
	}
	if err := json.Unmarshal(b, &res); err != nil {
		return nil, err
	}
	var out []string
	for _, r := range res.Results {
		if strings.HasPrefix(r.URL, "http") {
			out = append(out, r.URL)
		}
	}
	return out, nil
}

var mojeekResultRe = regexp.MustCompile(`<a[^>]*class="ob"[^>]*href="(https?://[^"]+)"`)

// searchMojeek reads Mojeek, an engine with its own index (not Google's or
// Bing's), so it surfaces small sites the others miss.
func (d *discoverer) searchMojeek(ctx context.Context, q string) ([]string, error) {
	b, status, err := d.webGet(ctx, "https://www.mojeek.com/search?q="+url.QueryEscape(q), 2<<20)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", status)
	}
	return parseMojeek(string(b)), nil
}

func parseMojeek(page string) []string {
	var out []string
	for _, m := range mojeekResultRe.FindAllStringSubmatch(page, -1) {
		h := html.UnescapeString(m[1])
		if strings.HasPrefix(h, "http") && !strings.Contains(h, "mojeek.com") {
			out = append(out, h)
		}
	}
	return out
}

// gitlabFiles searches GitLab's projects for a word and returns the raw
// addresses of their list-like files.
func (d *discoverer) gitlabFiles(ctx context.Context, q string) ([]string, error) {
	b, status, err := d.apiGet(ctx, "https://gitlab.com/api/v4/projects?order_by=last_activity_at&per_page=50&search="+url.QueryEscape(q), 4<<20)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", status)
	}
	var projects []struct {
		ID            int    `json:"id"`
		Path          string `json:"path_with_namespace"`
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal(b, &projects); err != nil {
		return nil, err
	}
	var out []string
	for _, p := range projects {
		if ctx.Err() != nil {
			break
		}
		if p.DefaultBranch == "" {
			continue // an empty project
		}
		tb, st, err := d.apiGet(ctx, "https://gitlab.com/api/v4/projects/"+strconv.Itoa(p.ID)+"/repository/tree?recursive=true&per_page=100", 4<<20)
		if err != nil || st != http.StatusOK {
			continue
		}
		var tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		}
		if json.Unmarshal(tb, &tree) != nil {
			continue
		}
		for _, t := range tree {
			if t.Type == "blob" && listLike(t.Path) {
				out = append(out, "https://gitlab.com/"+p.Path+"/-/raw/"+p.DefaultBranch+"/"+t.Path)
			}
		}
	}
	return out, nil
}

// codebergFiles does the same on Codeberg (Gitea's API).
func (d *discoverer) codebergFiles(ctx context.Context, q string) ([]string, error) {
	b, status, err := d.apiGet(ctx, "https://codeberg.org/api/v1/repos/search?sort=updated&order=desc&limit=50&q="+url.QueryEscape(q), 4<<20)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", status)
	}
	var res struct {
		Data []struct {
			FullName      string `json:"full_name"`
			DefaultBranch string `json:"default_branch"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &res); err != nil {
		return nil, err
	}
	var out []string
	for _, r := range res.Data {
		if ctx.Err() != nil {
			break
		}
		if r.DefaultBranch == "" {
			continue // an empty repository
		}
		tb, st, err := d.apiGet(ctx, "https://codeberg.org/api/v1/repos/"+r.FullName+"/git/trees/"+url.PathEscape(r.DefaultBranch)+"?recursive=true&per_page=1000", 4<<20)
		if err != nil || st != http.StatusOK {
			continue
		}
		var tree struct {
			Tree []struct {
				Path string `json:"path"`
				Type string `json:"type"`
			} `json:"tree"`
		}
		if json.Unmarshal(tb, &tree) != nil {
			continue
		}
		for _, t := range tree.Tree {
			if t.Type == "blob" && listLike(t.Path) {
				out = append(out, "https://codeberg.org/"+r.FullName+"/raw/branch/"+r.DefaultBranch+"/"+t.Path)
			}
		}
	}
	return out, nil
}

// listLike says a file in a repository may be a server list.
func listLike(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt", ".yaml", ".yml", ".json", ".conf", ".ovpn", "":
		return !strings.HasPrefix(path, ".") && !strings.Contains(path, "/.") && !strings.EqualFold(filepath.Base(path), "LICENSE")
	}
	return false
}

// repoQueries are the words the code-host searches use, a slice per run.
var repoQueries = []string{"v2ray", "vless", "vmess", "hysteria", "shadowsocks", "trojan", "clash", "sing-box", "proxy subscription", "free vpn", "wireguard config", "openvpn"}

// webSearch runs this hour's slice of the queries through every engine and
// returns the pages to read and the list files found on other code hosts.
func (d *discoverer) webSearch(ctx context.Context, hour int) (pages, files []string) {
	queries := rotate(webQueries, hour, webPerRun)
	engines := []struct {
		name string
		fn   func(context.Context, string) ([]string, error)
	}{
		{"duckduckgo", d.searchDDG}, {"bing", d.searchBing}, {"marginalia", d.searchMarginalia}, {"reddit", d.searchReddit},
		{"searx", d.searchSearx}, {"mojeek", d.searchMojeek},
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, e := range engines {
		wg.Add(1)
		go func(name string, fn func(context.Context, string) ([]string, error)) {
			defer wg.Done()
			n := 0
			// Counted however the loop ends: a refusal ends it early.
			defer func() {
				mu.Lock()
				d.stats["web_"+name] += n
				mu.Unlock()
			}()
			for _, q := range queries {
				if ctx.Err() != nil {
					return
				}
				res, err := fn(ctx, q)
				if err != nil {
					log.Printf("discover: %s %q: %v", name, q, err)
					// An engine that refuses is left alone for this run.
					return
				}
				mu.Lock()
				pages = append(pages, res...)
				mu.Unlock()
				n += len(res)
				// Engines dislike a burst of queries.
				select {
				case <-time.After(3 * time.Second):
				case <-ctx.Done():
					return
				}
			}
		}(e.name, e.fn)
	}
	hosts := []struct {
		name string
		fn   func(context.Context, string) ([]string, error)
	}{{"gitlab", d.gitlabFiles}, {"codeberg", d.codebergFiles}}
	for _, h := range hosts {
		wg.Add(1)
		go func(name string, fn func(context.Context, string) ([]string, error)) {
			defer wg.Done()
			n := 0
			defer func() {
				mu.Lock()
				d.stats["web_"+name] += n
				mu.Unlock()
			}()
			for _, q := range rotate(repoQueries, hour, 3) {
				if ctx.Err() != nil {
					return
				}
				res, err := fn(ctx, q)
				if err != nil {
					log.Printf("discover: %s %q: %v", name, q, err)
					return
				}
				mu.Lock()
				files = append(files, res...)
				mu.Unlock()
				n += len(res)
			}
		}(h.name, h.fn)
	}
	wg.Wait()
	return pages, files
}

// readPages fetches result pages and returns the lists and channels they
// mention and the pages that themselves hold servers (candidate sources).
func (d *discoverer) readPages(ctx context.Context, pages []string) (subs, channels, withServers []string) {
	seen := map[string]bool{}
	var list []string
	for _, p := range pages {
		p = strings.TrimSpace(p)
		u, err := url.Parse(p)
		if err != nil || u.Host == "" || seen[p] || skipHost(u.Host) {
			continue
		}
		seen[p] = true
		list = append(list, p)
	}
	if len(list) > 600 {
		list = list[:600]
	}
	var mu sync.Mutex
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, p := range list {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(p string) {
			defer func() { <-sem; wg.Done() }()
			b, status, err := d.webGet(ctx, p, 6<<20)
			if err != nil || status != http.StatusOK {
				return
			}
			text := string(b)
			s, c := mentions(text)
			n := 0
			node.EachText(text, func(*node.Node) { n++ })
			mu.Lock()
			subs, channels = append(subs, s...), append(channels, c...)
			if n >= minSourceLinks {
				withServers = append(withServers, p)
			}
			mu.Unlock()
		}(p)
	}
	wg.Wait()
	return subs, channels, withServers
}

// skipHost names sites whose pages are never sources: the engines, video
// and social sites, app stores.
func skipHost(h string) bool {
	h = strings.ToLower(h)
	for _, s := range []string{"duckduckgo.com", "bing.com", "google.", "youtube.com", "youtu.be", "facebook.com", "instagram.com",
		"twitter.com", "x.com", "tiktok.com", "apple.com", "microsoft.com", "wikipedia.org", "amazon.", "linkedin.com"} {
		// The site or a subdomain of it; a name ending in a dot stands for
		// the site under any top-level domain (google.de, amazon.co.uk). A
		// plain substring would also take v2rayx.com for x.com.
		if strings.HasSuffix(s, ".") {
			if strings.HasPrefix(h, s) || strings.Contains(h, "."+s) {
				return true
			}
		} else if h == s || strings.HasSuffix(h, "."+s) {
			return true
		}
	}
	return false
}

// canonicalPage keeps a web page's address as a source: unlike canonical,
// a site's front page counts (many free-server sites put them there).
func canonicalPage(u string) string {
	p, err := url.Parse(strings.TrimSpace(u))
	if err != nil || (p.Scheme != "https" && p.Scheme != "http") || p.Host == "" || skipHost(p.Host) {
		return ""
	}
	if c := canonical(u); c != "" {
		return c
	}
	p.Scheme = "https"
	p.Fragment, p.RawQuery = "", ""
	return p.String()
}
