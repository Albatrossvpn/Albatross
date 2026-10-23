package engine

import (
	"bufio"
	"compress/zlib"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Sites of the bypassed region that are missing from its domain list go
// through the VPN in proxy mode, where the core is handed names rather than
// addresses, so the list of the region's addresses never applies to them;
// many of those sites refuse foreign visitors. While connected, the names
// the VPN carries are looked up through the VPN, and those that resolve to
// addresses of the region are added to a list of their own. The core reloads
// that list by itself, so from the next connection on they go direct. The
// local resolver is never asked (it would see every site the user opens),
// and a failed lookup changes nothing.
const (
	maxLearned = 5000
	// learnEvery is how often the connection list is read while traffic
	// flows; nothing is read while idle.
	learnEvery = 5 * time.Second
	// recheckAfter is how long a name found not to be Iranian is left alone.
	recheckAfter = 24 * time.Hour
)

// dohServers answer DNS-over-HTTPS JSON queries. They are reached through
// the VPN.
var dohServers = []string{"https://cloudflare-dns.com/dns-query", "https://dns.google/resolve"}

type learner struct {
	path     string
	suffixes []string
	ranges   []ipRange

	mu      sync.Mutex
	learned map[string]bool
	checked map[string]time.Time
	queue   chan string
}

// writeLearned writes the learned list in sing-box's rule-set source format.
func writeLearned(path string, names []string) error {
	rules := []any{}
	if len(names) > 0 {
		sort.Strings(names)
		rules = append(rules, map[string]any{"domain": names})
	}
	b, err := json.Marshal(map[string]any{"version": 3, "rules": rules})
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readLearned(path string) []string {
	var f struct {
		Rules []struct {
			Domain []string `json:"domain"`
		} `json:"rules"`
	}
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &f) != nil {
		return nil
	}
	var out []string
	for _, r := range f.Rules {
		out = append(out, r.Domain...)
	}
	return out
}

// newLearner loads the region's learned list and address ranges. It returns
// nil when the ranges cannot be read.
func newLearner(rulesDir string, r region) *learner {
	ranges, err := readIPRuleSet(r.geoIPFile(rulesDir))
	if err != nil || len(ranges) == 0 {
		return nil
	}
	l := &learner{
		path:     r.learnedFile(rulesDir),
		suffixes: r.Suffixes,
		ranges:   ranges,
		learned:  map[string]bool{},
		checked:  map[string]time.Time{},
		queue:    make(chan string, 64),
	}
	for _, n := range readLearned(l.path) {
		l.learned[n] = true
	}
	return l
}

// want reports whether host should be looked up now, and marks it.
func (l *learner) want(host string) bool {
	if host == "" || strings.Contains(host, ":") {
		return false
	}
	for _, s := range l.suffixes {
		if strings.HasSuffix(host, s) {
			return false
		}
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.learned[host] || len(l.learned) >= maxLearned {
		return false
	}
	if t, ok := l.checked[host]; ok && time.Since(t) < recheckAfter {
		return false
	}
	l.checked[host] = time.Now()
	return true
}

// local reports whether any of addrs is in the region.
func (l *learner) local(addrs []netip.Addr) bool {
	for _, a := range addrs {
		if ipIn(l.ranges, a) {
			return true
		}
	}
	return false
}

// add records host as local and rewrites the list.
func (l *learner) add(host string) error {
	l.mu.Lock()
	l.learned[host] = true
	names := make([]string, 0, len(l.learned))
	for n := range l.learned {
		names = append(names, n)
	}
	l.mu.Unlock()
	return writeLearned(l.path, names)
}

// learnLocal follows the names a connection carries through the VPN and
// learns which of them belong to the bypassed region.
func (e *Engine) learnLocal(ctx context.Context, c *connection, r region) {
	l := newLearner(filepath.Join(e.dir, "rules"), r)
	if l == nil {
		return
	}
	for range 2 {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case host := <-l.queue:
					addrs, err := lookupThrough(ctx, e.proxyClient(), host)
					if err != nil || !l.local(addrs) {
						continue
					}
					if l.add(host) == nil {
						e.logf("info", "%q is a local site; it will open directly from now on.", host)
					}
				}
			}
		}()
	}
	t := time.NewTicker(learnEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if c.idle() {
			continue
		}
		for _, host := range c.proxiedHosts(ctx) {
			if !l.want(host) {
				continue
			}
			select {
			case l.queue <- host:
			default:
				// Busy; the name is tried again after recheckAfter.
			}
		}
	}
}

// proxiedHosts lists the names of open connections going through the VPN.
func (c *connection) proxiedHosts(ctx context.Context) []string {
	var list struct {
		Connections []struct {
			Metadata struct {
				Host string `json:"host"`
			} `json:"metadata"`
			Chains []string `json:"chains"`
		} `json:"connections"`
	}
	if _, err := c.api(ctx, http.MethodGet, "/connections", nil, &list, 3*time.Second); err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, conn := range list.Connections {
		h := strings.ToLower(strings.TrimSuffix(conn.Metadata.Host, "."))
		if h == "" || seen[h] || len(conn.Chains) == 0 || conn.Chains[len(conn.Chains)-1] != "proxy" {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}

// lookupThrough resolves host's IPv4 addresses with DNS-over-HTTPS through
// client.
func lookupThrough(ctx context.Context, client *http.Client, host string) ([]netip.Addr, error) {
	if client == nil {
		return nil, errors.New("not connected")
	}
	var lastErr error
	for _, s := range dohServers {
		addrs, err := dohQuery(ctx, client, s, host)
		if err == nil {
			return addrs, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func dohQuery(ctx context.Context, client *http.Client, server, host string) ([]netip.Addr, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server+"?"+url.Values{"name": {host}, "type": {"A"}}.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/dns-json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}
	var r struct {
		Status int `json:"Status"`
		Answer []struct {
			Type int    `json:"type"`
			Data string `json:"data"`
		} `json:"Answer"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&r); err != nil {
		return nil, err
	}
	var out []netip.Addr
	for _, a := range r.Answer {
		if a.Type != 1 {
			continue
		}
		if ip, err := netip.ParseAddr(a.Data); err == nil {
			out = append(out, ip)
		}
	}
	return out, nil
}

// ipRange is an inclusive range of addresses.
type ipRange struct{ from, to netip.Addr }

// ipIn reports whether a is in ranges, which are sorted and do not overlap.
func ipIn(ranges []ipRange, a netip.Addr) bool {
	a = a.Unmap()
	i := sort.Search(len(ranges), func(i int) bool { return ranges[i].from.Compare(a) > 0 })
	return i > 0 && ranges[i-1].to.Compare(a) >= 0
}

// readIPRuleSet reads the address ranges of a binary sing-box rule set that
// holds only ip_cidr rules, such as geoip-cn.srs. The format is sing-box's
// common/srs: "SRS", a version byte, then zlib holding a count of rules, each
// a type byte (0 = default), items (6 = an IP set) and 0xFF, then an invert
// flag.
func readIPRuleSet(path string) ([]ipRange, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var head [4]byte
	if _, err := io.ReadFull(f, head[:]); err != nil {
		return nil, err
	}
	if string(head[:3]) != "SRS" {
		return nil, errors.New("not a rule set")
	}
	z, err := zlib.NewReader(f)
	if err != nil {
		return nil, err
	}
	r := bufio.NewReader(z)
	rules, err := binary.ReadUvarint(r)
	if err != nil {
		return nil, err
	}
	var out []ipRange
	for range rules {
		if t, err := r.ReadByte(); err != nil || t != 0 {
			return nil, errors.New("unsupported rule")
		}
		for {
			item, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			if item == 0xFF {
				break
			}
			if item != 6 {
				return nil, errors.New("unsupported rule item")
			}
			if v, err := r.ReadByte(); err != nil || v != 1 {
				return nil, errors.New("unsupported IP set")
			}
			var n uint64
			if err := binary.Read(r, binary.BigEndian, &n); err != nil {
				return nil, err
			}
			for range n {
				from, err := readSetAddr(r)
				if err != nil {
					return nil, err
				}
				to, err := readSetAddr(r)
				if err != nil {
					return nil, err
				}
				out = append(out, ipRange{from, to})
			}
		}
		if _, err := r.ReadByte(); err != nil { // invert
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].from.Compare(out[j].from) < 0 })
	return out, nil
}

func readSetAddr(r *bufio.Reader) (netip.Addr, error) {
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return netip.Addr{}, err
	}
	if n != 4 && n != 16 {
		return netip.Addr{}, errors.New("bad address")
	}
	var b [16]byte
	if _, err := io.ReadFull(r, b[:n]); err != nil {
		return netip.Addr{}, err
	}
	a, _ := netip.AddrFromSlice(b[:n])
	return a.Unmap(), nil
}
