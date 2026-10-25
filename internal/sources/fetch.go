package sources

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
		attempts = append(attempts, attempt{"direct", u, f.Direct, 0})
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
		clients = append(clients, f.Direct)
		if f.Private != nil {
			clients = append(clients, f.Private)
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
		for _, u := range Addresses(url) {
			switch {
			case IsDomestic(u):
				// A mirror inside the country: first when foreign addresses
				// are cut, last otherwise; plain and direct.
				n++
				a := attempt{"domestic" + strconv.Itoa(n), u, f.Direct, 20 * time.Second}
				if mode == ModeDomestic {
					attempts = append([]attempt{a}, attempts...)
				} else {
					attempts = append(attempts, a)
				}
			case IsWorker(u):
				// The project's own storage: through the VPN, or the
				// private client, never a plain direct call (IsWorker).
				c := proxy
				if c == nil {
					c = f.Private
				}
				if c != nil && mode != ModeDomestic {
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
		eip, err := f.getPage(ctx, c, api+"/3/config/eip-service.json")
		if err == nil {
			var cert, ca string
			if cert, err = f.getPage(ctx, c, api+"/3/cert"); err == nil {
				ca, err = f.getPage(ctx, c, caURL)
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
