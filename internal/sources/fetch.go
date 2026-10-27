package sources

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Albatrossvpn/vp/internal/node"
)

const (
	vpnGateURL  = "https://www.vpngate.net/api/iphone/"
	maxBodySize = 48 << 20
	userAgent   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Safari/537.36"
)

// Fetcher downloads sources, trying the local proxy (when connected), a
// direct connection and public CDN mirrors of GitHub in turn; the app's own
// files start at the project's own storage (Addresses).
type Fetcher struct {
	// Mode, when set, says how to reach sources now (ModeDomestic while
	// foreign addresses are cut).
	Mode   func() Mode
	Direct *http.Client
	Proxy  func() *http.Client // returns nil while disconnected
	// Private reaches the Worker while disconnected without showing its
	// name (see IsWorker); nil leaves the Worker out then.
	Private  *http.Client
	CacheDir string
	// Now is the clock for dated addresses (see fetchDated); nil is time.Now.
	Now func() time.Time

	bytes atomic.Int64
}

// Result is the outcome of fetching one source.
type Result struct {
	Source    Source
	Text      string
	FromCache bool // served from disk because nothing changed or all paths failed
	Via       string
	Err       error
}

// BytesDownloaded is the total network traffic used by the fetcher.
func (f *Fetcher) BytesDownloaded() int64 { return f.bytes.Load() }

type cacheMeta struct {
	URL          string    `json:"url"`
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"last_modified,omitempty"`
	FetchedAt    time.Time `json:"fetched_at"`
}

type attempt struct {
	via     string
	url     string
	client  *http.Client
	timeout time.Duration // 0: the usual
}

// Mode is how the fetcher reaches sources: as usual, or as a network
// where only domestic addresses answer allows.
type Mode int

const (
	ModeNormal   Mode = iota
	ModeDomestic      // foreign addresses are cut: domestic mirrors first, short tries elsewhere
)

// Fetch downloads one source. When every path fails it falls back to the
// last copy on disk and reports the error alongside it.
func (f *Fetcher) Fetch(ctx context.Context, s Source) Result {
	switch {
	case s.Kind == KindFeed:
		return f.fetchFeed(ctx, s)
	case s.Kind == KindSubscription && strings.Contains(s.URL, "{YYYY}"):
		return f.fetchDated(ctx, s)
	}
	return f.fetchOne(ctx, s)
}

func (f *Fetcher) fetchOne(ctx context.Context, s Source) Result {
	res := Result{Source: s}
	var attempts []attempt
	proxy := (*http.Client)(nil)
	if f.Proxy != nil {
		proxy = f.Proxy()
	}
	mode := ModeNormal
	if f.Mode != nil {
		mode = f.Mode()
	}
	switch s.Kind {
	case KindTelegram:
		u := "https://t.me/s/" + s.URL
		if mode == ModeDomestic && proxy == nil {
			// A foreign site on a network that cuts them: the cache.
			return f.fallback(res, errors.New("foreign addresses are cut"))
		}
		if proxy != nil {
			attempts = append(attempts, attempt{"proxy", u, proxy, 0})
		}
		if mode != ModeDomestic {
			attempts = append(attempts, attempt{"direct", u, f.Direct, 0})
		}
		text, via, err := f.fetchTelegram(ctx, attempts)
		if err == nil {
			res.Text, res.Via = text, via
			f.store(s, cacheMeta{URL: u, FetchedAt: time.Now()}, text)
			return res
		}
		return f.fallback(res, err)
	case KindLEAP:
		if mode == ModeDomestic && proxy == nil {
			return f.fallback(res, errors.New("foreign addresses are cut"))
		}
		var clients []*http.Client
		if proxy != nil {
			clients = append(clients, proxy)
		}
		if mode != ModeDomestic {
			clients = append(clients, f.Direct, f.Private)
		}
		text, err := f.fetchLEAP(ctx, s.URL, clients)
		if err == nil {
			res.Text, res.Via = text, "direct"
			f.store(s, cacheMeta{URL: s.URL, FetchedAt: time.Now()}, text)
			return res
		}
		return f.fallback(res, err)
	default:
		// Outline dynamic keys name an HTTPS address with their own scheme.
		url := s.URL
		if strings.HasPrefix(url, "ssconf://") {
			url = "https://" + strings.TrimPrefix(url, "ssconf://")
		}
		n := 0
		var dom []attempt
		for _, u := range Addresses(url) {
			switch {
			case IsDomestic(u):
				// A mirror inside the country: plain and direct.
				n++
				dom = append(dom, attempt{"domestic" + strconv.Itoa(n), u, f.Direct, 20 * time.Second})
			case IsWorker(u):
				// The project's own storage: through the VPN, or the
				// private client, never a plain direct call (IsWorker).
				// Where foreign addresses are cut only the VPN gets there.
				c := proxy
				if c == nil && mode != ModeDomestic {
					c = f.Private
				}
				if c != nil {
					n++
					attempts = append(attempts, attempt{"mirror" + strconv.Itoa(n), u, c, 0})
				}
			case u == url:
				if proxy != nil {
					attempts = append(attempts, attempt{"proxy", url, proxy, 0})
				}
				t := time.Duration(0)
				if mode == ModeDomestic {
					t = 8 * time.Second
				}
				attempts = append(attempts, attempt{"direct", url, f.Direct, t})
			default:
				// Foreign CDN copies: pointless where foreign addresses
				// are cut, except GitHub's API, which stayed open.
				if mode == ModeDomestic && !strings.HasPrefix(u, githubAPI) {
					continue
				}
				n++
				t := time.Duration(0)
				if mode == ModeDomestic {
					t = 8 * time.Second
				}
				attempts = append(attempts, attempt{"mirror" + strconv.Itoa(n), u, f.Direct, t})
			}
		}
		// The domestic mirrors, in their order: first when foreign
		// addresses are cut, last otherwise.
		if mode == ModeDomestic {
			attempts = append(dom, attempts...)
		} else {
			attempts = append(attempts, dom...)
		}
		// The proxy attempt belongs first whatever the mode.
		for i, a := range attempts {
			if a.via == "proxy" && i > 0 {
				attempts = append([]attempt{a}, append(attempts[:i:i], attempts[i+1:]...)...)
				break
			}
		}
	}
	meta, _ := f.loadMeta(s)
	var lastErr error
	for _, a := range attempts {
		if ctx.Err() != nil {
			return f.fallback(res, ctx.Err())
		}
		etag, lm := "", ""
		if meta != nil && meta.URL == a.url {
			etag, lm = meta.ETag, meta.LastModified
		}
		body, newMeta, notModified, err := f.get(ctx, a, etag, lm)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", a.via, err)
			continue
		}
		res.Via = a.via
		if notModified {
			if text, ok := f.loadBody(s); ok {
				res.Text, res.FromCache = text, true
				meta.FetchedAt = time.Now()
				f.saveMeta(s, *meta)
				return res
			}
			// Cache vanished; fetch again without validators.
			body, newMeta, _, err = f.get(ctx, a, "", "")
			if err != nil {
				lastErr = err
				continue
			}
		}
		res.Text = body
		f.store(s, newMeta, body)
		return res
	}
	if lastErr == nil {
		lastErr = errors.New("no way to reach source")
	}
	return f.fallback(res, lastErr)
}

func (f *Fetcher) fallback(res Result, err error) Result {
	if text, ok := f.loadBody(res.Source); ok {
		res.Text, res.FromCache = text, true
	}
	res.Err = err
	return res
}

func (f *Fetcher) get(ctx context.Context, a attempt, etag, lastMod string) (string, cacheMeta, bool, error) {
	timeout := 45 * time.Second
	if a.via != "proxy" && a.via != "direct" {
		timeout = 60 * time.Second
	}
	if a.timeout > 0 {
		timeout = a.timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.url, nil)
	if err != nil {
		return "", cacheMeta{}, false, err
	}
	req.Header.Set("User-Agent", userAgent)
	if strings.HasPrefix(a.url, githubAPI) {
		req.Header.Set("Accept", "application/vnd.github.raw")
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastMod != "" {
		req.Header.Set("If-Modified-Since", lastMod)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return "", cacheMeta{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return "", cacheMeta{}, true, nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", cacheMeta{}, false, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	f.bytes.Add(int64(len(b)))
	if err != nil {
		return "", cacheMeta{}, false, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return "", cacheMeta{}, false, errors.New("empty body")
	}
	return string(b), cacheMeta{
		URL:          a.url,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		FetchedAt:    time.Now(),
	}, false, nil
}

var tgPostRe = regexp.MustCompile(`data-post="[^"/]+/(\d+)"`)

// fetchTelegram reads the two newest pages of a channel's web preview.
func (f *Fetcher) fetchTelegram(ctx context.Context, attempts []attempt) (string, string, error) {
	var lastErr error
	for _, a := range attempts {
		page, err := f.getPage(ctx, a.client, a.url)
		if err != nil {
			lastErr = err
			continue
		}
		text := page
		if ids := tgPostRe.FindAllStringSubmatch(page, -1); len(ids) > 0 {
			oldest := ids[0][1]
			if older, err := f.getPage(ctx, a.client, a.url+"?before="+oldest); err == nil {
				text += "\n" + older
			}
		}
		return text, a.via, nil
	}
	return "", "", lastErr
}

func (f *Fetcher) getPage(ctx context.Context, c *http.Client, url string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	f.bytes.Add(int64(len(b)))
	if err != nil {
		return "", err
	}
	if !strings.Contains(string(b), "tgme_widget_message") {
		return "", errors.New("not a channel preview")
	}
	return string(b), nil
}

var rawGitHubRe = regexp.MustCompile(`^https?://raw\.githubusercontent\.com/([^/]+)/([^/]+)/([^/]+)/(.+)$`)

// WorkerMirror is the project's own storage as its Workers serve it (the
// account service and the website, backend/src/index.js and site/src): their
// addresses, comma separated, or empty while there are none. Both serve the
// app's own files at /list/<branch>/<path>.
var WorkerMirror = ""

// DomesticMirror lists mirrors inside the country (deploy/server MIRROR
// mode), comma separated, set at build time: plain http bases such as
// http://5.6.7.8:8081/<token>, which serve the app's own files at
// /list/<branch>/<path> and the installers at /dl/<name>. Only these bases
// may be plain http: the lists they serve are sealed or checked, and the
// installers are checked against their SHA-256.
var DomesticMirror = ""

// ExtraMirrors, when set, returns domestic mirror bases learned later (a
// share code, a setting).
var ExtraMirrors func() []string

// DomesticBases returns every domestic mirror base, trimmed.
func DomesticBases() []string {
	var out []string
	add := func(list []string) {
		for _, b := range list {
			b = strings.TrimRight(strings.TrimSpace(b), "/")
			if b != "" && (strings.HasPrefix(b, "http://") || strings.HasPrefix(b, "https://")) {
				out = append(out, b)
			}
		}
	}
	add(strings.Split(DomesticMirror, ","))
	if ExtraMirrors != nil {
		add(ExtraMirrors())
	}
	return out
}

// IsDomestic says whether u is on a domestic mirror.
func IsDomestic(u string) bool {
	for _, b := range DomesticBases() {
		if strings.HasPrefix(u, b+"/") {
			return true
		}
	}
	return false
}

// Mirrors returns CDN copies of a raw GitHub URL, which often stay
// reachable when raw.githubusercontent.com is filtered.
func Mirrors(u string) []string {
	m := rawGitHubRe.FindStringSubmatch(u)
	if m == nil {
		return nil
	}
	owner, repo, branch, path := m[1], m[2], m[3], m[4]
	out := []string{
		fmt.Sprintf("https://cdn.jsdelivr.net/gh/%s/%s@%s/%s", owner, repo, branch, path),
		fmt.Sprintf("https://fastly.jsdelivr.net/gh/%s/%s@%s/%s", owner, repo, branch, path),
		fmt.Sprintf("https://raw.githack.com/%s/%s/%s/%s", owner, repo, branch, path),
		fmt.Sprintf("https://gcore.jsdelivr.net/gh/%s/%s@%s/%s", owner, repo, branch, path),
	}
	// api.github.com stayed open through Iran's allowlist periods while
	// github.com and raw were closed. Anonymous use allows 60 requests an
	// hour per address, so only the app's own files go this way.
	if strings.EqualFold(owner+"/"+repo, ownRepo) {
		out = append(out, fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s?ref=%s", owner, repo, path, branch))
		for _, w := range strings.Split(WorkerMirror, ",") {
			if w = strings.TrimRight(strings.TrimSpace(w), "/"); w != "" {
				out = append(out, w+"/list/"+branch+"/"+path)
			}
		}
		for _, b := range DomesticBases() {
			out = append(out, b+"/list/"+branch+"/"+path)
		}
	}
	return out
}

// Addresses lists where u can be downloaded, in the order to try. The app's
// own files (the collector's lists, the update manifest, the ad list) live
// in the project's own storage, served by its Workers (WorkerMirror), so
// those come first; GitHub, where the files were first published, and its
// CDN mirrors follow while the repository is public. Any other file starts
// at u itself.
func Addresses(u string) []string {
	var own, rest, dom []string
	for _, m := range Mirrors(u) {
		switch {
		case IsWorker(m):
			own = append(own, m)
		case IsDomestic(m):
			dom = append(dom, m)
		default:
			rest = append(rest, m)
		}
	}
	return append(append(append(own, u), rest...), dom...)
}

// IsWorker says whether u is on the Worker, whose name is also the account
// service's: a plain direct call to it shows the network who uses the app.
func IsWorker(u string) bool {
	for _, w := range strings.Split(WorkerMirror, ",") {
		if w = strings.TrimRight(strings.TrimSpace(w), "/"); w != "" && strings.HasPrefix(u, w+"/") {
			return true
		}
	}
	return false
}

// ownRepo is the app's own repository, where the collector publishes.
const ownRepo = "Albatrossvpn/Albatross"

// githubAPI is the prefix of api.github.com contents URLs, which return
// the raw file when asked for it.
const githubAPI = "https://api.github.com/repos/"

func (f *Fetcher) paths(s Source) (body, meta string) {
	base := filepath.Join(f.CacheDir, string(s.Kind)+"-"+s.ID)
	return base + ".gz", base + ".json"
}

func (f *Fetcher) store(s Source, meta cacheMeta, text string) {
	if f.CacheDir == "" {
		return
	}
	bodyPath, _ := f.paths(s)
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	zw.Write([]byte(text))
	zw.Close()
	if writeAtomic(bodyPath, buf.Bytes()) == nil {
		f.saveMeta(s, meta)
	}
}

func (f *Fetcher) saveMeta(s Source, meta cacheMeta) {
	if f.CacheDir == "" {
		return
	}
	_, metaPath := f.paths(s)
	b, _ := json.Marshal(meta)
	writeAtomic(metaPath, b)
}

func (f *Fetcher) loadMeta(s Source) (*cacheMeta, error) {
	if f.CacheDir == "" {
		return nil, os.ErrNotExist
	}
	_, metaPath := f.paths(s)
	b, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, err
	}
	var m cacheMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (f *Fetcher) loadBody(s Source) (string, bool) {
	if f.CacheDir == "" {
		return "", false
	}
	bodyPath, _ := f.paths(s)
	fh, err := os.Open(bodyPath)
	if err != nil {
		return "", false
	}
	defer fh.Close()
	zr, err := gzip.NewReader(fh)
	if err != nil {
		return "", false
	}
	b, err := io.ReadAll(io.LimitReader(zr, maxBodySize))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// Cached returns the last downloaded body of a source, if any.
func (f *Fetcher) Cached(s Source) (string, bool) { return f.loadBody(s) }

// CachedAt reports when a source was last fetched successfully.
func (f *Fetcher) CachedAt(s Source) time.Time {
	m, err := f.loadMeta(s)
	if err != nil {
		return time.Time{}
	}
	return m.FetchedAt
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// leapMax bounds each LEAP answer (Riseup's gateway list is about 10 KB).
const leapMax = 1 << 20

// fetchLEAP downloads a LEAP provider's gateway list, an anonymous client
// certificate and the provider's CA, and joins them as a node.LEAPDoc.
func (f *Fetcher) fetchLEAP(ctx context.Context, api string, clients []*http.Client) (string, error) {
	caURL, ok := leapProviders[api]
	if !ok {
		return "", fmt.Errorf("unknown LEAP provider")
	}
	var lastErr error
	for _, c := range clients {
		if c == nil {
			continue
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		// getBody, not getPage: getPage takes nothing but a channel preview,
		// and with it no LEAP provider ever answered.
		eip, _, err := f.getBody(ctx, c, api+"/3/config/eip-service.json", leapMax)
		if err == nil {
			var cert, ca string
			if cert, _, err = f.getBody(ctx, c, api+"/3/cert", leapMax); err == nil {
				ca, _, err = f.getBody(ctx, c, caURL, leapMax)
			}
			if err == nil {
				if !json.Valid([]byte(eip)) || !strings.Contains(cert, "PRIVATE KEY") || !strings.Contains(ca, "CERTIFICATE") {
					err = errors.New("unexpected LEAP answer")
				} else {
					b, _ := json.Marshal(node.LEAPDoc{Provider: LEAPName(api), EIP: json.RawMessage(eip), Cert: cert, CA: ca})
					return string(b), nil
				}
			}
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no way to reach source")
	}
	return "", lastErr
}

func (f *Fetcher) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

// datedURL fills {YYYY}, {MM} and {DD} in an address with the day d.
func datedURL(tpl string, d time.Time) string {
	return strings.NewReplacer("{YYYY}", d.Format("2006"), "{MM}", d.Format("01"), "{DD}", d.Format("02")).Replace(tpl)
}

// datedDays are the days a dated address may name right now, newest first:
// the date in China (UTC+8, which several of these sites publish by), the
// date in UTC, and the day before.
func datedDays(now time.Time) []time.Time {
	utc := now.UTC()
	cn := utc.Add(8 * time.Hour)
	return []time.Time{cn, utc, cn.AddDate(0, 0, -1)}
}

// fetchDated reads a site that publishes one file a day under an address
// with the date in it (.../uploads/{YYYY}/{MM}/{YYYY}{MM}{DD}.txt): the
// newest day that answers wins. The copy on disk is the source's, whatever
// the day.
func (f *Fetcher) fetchDated(ctx context.Context, s Source) Result {
	var last Result
	tried := map[string]bool{}
	for _, d := range datedDays(f.now()) {
		u := datedURL(s.URL, d)
		if tried[u] {
			continue
		}
		tried[u] = true
		day := s
		day.URL = u
		r := f.fetchOne(ctx, day)
		r.Source = s
		if r.Err == nil {
			return r
		}
		last = r
	}
	return last
}

const (
	feedEntries = 4       // the newest entries of a feed that are read
	feedLinks   = 8       // the most links followed from one feed
	feedPageMax = 4 << 20 // the most read from one linked page
)

var feedLinkRe = regexp.MustCompile(`https?://[^\s<>"'\x60)\]]+`)

// feedSkipHosts are sites a description links to that never hold a list.
var feedSkipHosts = []string{
	"youtube.com", "youtu.be", "google.com", "googleusercontent.com", "ggpht.com", "ytimg.com",
	"facebook.com", "instagram.com", "twitter.com", "x.com", "t.me", "telegram.me", "telegram.org",
	"github.com", "apple.com", "microsoft.com", "amazon.com", "paypal.com", "patreon.com",
}

func feedSkipHost(host string) bool {
	host = strings.ToLower(host)
	for _, h := range feedSkipHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

// feedLinksOf returns the addresses in the newest entries of an RSS or Atom
// feed, newest first and without repeats, leaving out sites that never hold
// a list. A channel repeats its links in every video, so repeats are common.
func feedLinksOf(feed string) []string {
	var blocks []string
	for _, tag := range []string{"<entry", "<item"} {
		if parts := strings.Split(feed, tag)[1:]; len(parts) > 0 {
			blocks = parts
			break
		}
	}
	if len(blocks) > feedEntries {
		blocks = blocks[:feedEntries]
	}
	var out []string
	seen := map[string]bool{}
	for _, b := range blocks {
		for _, m := range feedLinkRe.FindAllString(html.UnescapeString(b), -1) {
			m = strings.TrimRight(m, ".,;:!?")
			u, err := neturl.Parse(m)
			if err != nil || u.Host == "" || feedSkipHost(u.Hostname()) || seen[m] {
				continue
			}
			seen[m] = true
			out = append(out, m)
		}
	}
	if len(out) > feedLinks {
		out = out[:feedLinks]
	}
	return out
}

// getBody downloads one address, following redirects, and returns the body
// with the address it ended at.
func (f *Fetcher) getBody(ctx context.Context, c *http.Client, u string, max int64) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if ct := strings.ToLower(resp.Header.Get("Content-Type")); strings.HasPrefix(ct, "image/") || strings.HasPrefix(ct, "video/") || strings.HasPrefix(ct, "audio/") || strings.HasPrefix(ct, "font/") || strings.Contains(ct, "zip") {
		return "", "", errors.New("not text: " + ct)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max))
	f.bytes.Add(int64(len(b)))
	if err != nil {
		return "", "", err
	}
	return string(b), resp.Request.URL.String(), nil
}

// fetchFeed reads a channel's feed (YouTube's videos.xml) and follows the
// lists its newest entries link to, one hop: some channels post a fresh
// paste of servers in every video. The text is the feed's lists joined; an
// address that answers with the servers in its own address (a redirect to
// ...?config=vless%3A%2F%2F...) adds the decoded address.
func (f *Fetcher) fetchFeed(ctx context.Context, s Source) Result {
	res := Result{Source: s}
	var proxy *http.Client
	if f.Proxy != nil {
		proxy = f.Proxy()
	}
	if f.Mode != nil && f.Mode() == ModeDomestic && proxy == nil {
		return f.fallback(res, errors.New("foreign addresses are cut"))
	}
	var clients []*http.Client
	if proxy != nil {
		clients = append(clients, proxy)
	}
	if f.Mode == nil || f.Mode() != ModeDomestic {
		clients = append(clients, f.Direct)
	}
	var feed string
	var client *http.Client
	var lastErr error
	for _, c := range clients {
		b, _, err := f.getBody(ctx, c, s.URL, 4<<20)
		if err != nil {
			lastErr = err
			continue
		}
		feed, client = b, c
		break
	}
	if client == nil {
		return f.fallback(res, lastErr)
	}
	var parts []string
	for _, l := range feedLinksOf(feed) {
		if ctx.Err() != nil {
			break
		}
		body, final, err := f.getBody(ctx, client, l, feedPageMax)
		if err != nil {
			continue
		}
		parts = append(parts, body)
		if final != l {
			if dec, err := neturl.QueryUnescape(final); err == nil {
				parts = append(parts, dec)
			}
		}
	}
	if len(parts) == 0 {
		return f.fallback(res, errors.New("no link in the feed answered"))
	}
	res.Text, res.Via = strings.Join(parts, "\n"), "direct"
	f.store(s, cacheMeta{URL: s.URL, FetchedAt: time.Now()}, res.Text)
	return res
}
