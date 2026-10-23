// Collect gathers every built-in source in one place, outside the filtered
// network, and writes a deduplicated list of servers that the apps fetch
// instead of downloading hundreds of sources themselves. It runs on a
// schedule in CI (.github/workflows/collect.yml).
//
// Output, in -out:
//
//	fresh.txt       servers from tested lists, Telegram and VPN Gate
//	all-1.txt ...   everything else, best first, split into -shards files
//	meta.json       when it ran and how many servers each file holds
//	tor.json        Tor's current bridges (built-in and recommended for Iran)
//
// Servers whose address does not answer at all are left out (UDP-based
// ones cannot be checked that way and are kept).
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cezomagoanlace29861-jpg/vp/internal/geo"
	"github.com/cezomagoanlace29861-jpg/vp/internal/node"
	"github.com/cezomagoanlace29861-jpg/vp/internal/sources"
)

type entry struct {
	link     string
	fresh    bool
	pop      int
	endpoint string // "" for UDP-based servers
	proto    string
}

type meta struct {
	Updated time.Time `json:"updated"`
	Sources int       `json:"sources"`
	Reached int       `json:"reached"`
	Found   int       `json:"found"`
	Dead    int       `json:"dead"`
	// Tested servers were connected through with the real cores; Passed
	// of them worked and lead fresh.txt, fastest first.
	Tested int            `json:"tested"`
	Passed int            `json:"passed"`
	Files  map[string]int `json:"files"`
	// Schemes counts every link scheme seen in the sources, usable or
	// not, so protocols the apps cannot run yet show up here.
	Schemes map[string]int `json:"schemes"`
	// Protocols counts the usable servers by protocol.
	Protocols map[string]int `json:"protocols"`
	// Channels counts the usable servers each Telegram channel posted;
	// channels that stay at zero can be dropped from the lists.
	Channels map[string]int `json:"channels"`
}

var schemeRe = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]{1,20})://[^\s"'<>]{8,}`)

func main() {
	out := flag.String("out", "servers", "output directory")
	shards := flag.Int("shards", 8, "number of all-N.txt files")
	probe := flag.Bool("probe", true, "drop servers whose address does not answer")
	probeConc := flag.Int("probe-concurrency", 1000, "parallel reachability probes")
	maxFresh := flag.Int("max-fresh", 60000, "most servers in fresh.txt; the rest go to the other files")
	limit := flag.Int("limit", 0, "use only the first N sources (for tests)")
	real := flag.Int("real", 8000, "connect through this many of the fresh servers with the cores in SING_BOX and XRAY; 0 skips")
	flag.Parse()

	srcs := sources.Collected()
	if *limit > 0 && *limit < len(srcs) {
		srcs = srcs[:*limit]
	}
	cache, err := os.MkdirTemp("", "collect")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(cache)
	f := &sources.Fetcher{
		Direct:   &http.Client{Timeout: 90 * time.Second},
		CacheDir: cache,
	}

	var mu sync.Mutex
	byKey := map[uint64]*entry{}
	reached := 0
	schemes, channels := map[string]int{}, map[string]int{}
	seenLinks := map[uint64]bool{}
	ctx := context.Background()
	sem := make(chan struct{}, 32)
	var wg sync.WaitGroup
	for _, s := range srcs {
		wg.Add(1)
		sem <- struct{}{}
		go func(s sources.Source) {
			defer wg.Done()
			defer func() { <-sem }()
			res := f.Fetch(ctx, s)
			if res.Err != nil || res.Text == "" {
				log.Printf("skip %s: %v", s.Label(), res.Err)
				return
			}
			fresh := s.Tier == sources.TierPriority
			var nodes []*node.Node
			if s.Kind == sources.KindVPNGate {
				for _, v := range node.ParseVPNGate(res.Text) {
					// The country travels in the name, where the app looks
					// for a hint.
					if flag := flagOf(v.Country); flag != "" && geo.Hint(v.Node.Name) == "" {
						v.Node.Name = flag + " " + v.Node.Name
						v.Node.Source = ""
					}
					nodes = append(nodes, v.Node)
				}
			} else {
				node.EachText(res.Text, func(n *node.Node) { nodes = append(nodes, n) })
			}
			links := schemeRe.FindAllStringSubmatch(res.Text, -1)
			mu.Lock()
			defer mu.Unlock()
			for _, m := range links {
				h := fnv.New64a()
				h.Write([]byte(m[0]))
				if k := h.Sum64(); !seenLinks[k] {
					seenLinks[k] = true
					schemes[strings.ToLower(m[1])]++
				}
			}
			reached++
			usable := 0
			for _, n := range nodes {
				if n.Engine() == node.EngineNone {
					continue
				}
				usable++
				k := node.Key64(n.Key())
				e := byKey[k]
				if e == nil {
					e = &entry{link: n.Link(), proto: n.Proto}
					if !n.UDPBased() {
						e.endpoint = n.Endpoint()
					}
					byKey[k] = e
				}
				e.pop++
				e.fresh = e.fresh || fresh
			}
			if s.Kind == sources.KindTelegram {
				channels[s.URL] = usable
			}
		}(s)
	}
	wg.Wait()
	log.Printf("%d of %d sources reached, %d servers", reached, len(srcs), len(byKey))

	list := make([]*entry, 0, len(byKey))
	for _, e := range byKey {
		list = append(list, e)
	}
	dead := 0
	if *probe {
		alive := probeAll(list, *probeConc)
		kept := list[:0]
		for _, e := range list {
			if e.endpoint == "" || alive[e.endpoint] {
				kept = append(kept, e)
			} else {
				dead++
			}
		}
		list = kept
		log.Printf("%d servers did not answer, %d kept", dead, len(list))
	}
	// Servers many lists carry tend to be the ones that keep working.
	sort.Slice(list, func(i, j int) bool {
		if list[i].pop != list[j].pop {
			return list[i].pop > list[j].pop
		}
		return list[i].link < list[j].link
	})
	protocols := map[string]int{}
	for _, e := range list {
		protocols[e.proto]++
	}
	var freshE, restE []*entry
	for _, e := range list {
		if e.fresh && len(freshE) < *maxFresh {
			freshE = append(freshE, e)
		} else {
			restE = append(restE, e)
		}
	}
	passed, tested := realTest(freshE, *real)
	freshE, failedE := orderFresh(freshE, passed, tested)
	restE = append(failedE, restE...)
	links := func(es []*entry) []string {
		out := make([]string, len(es))
		for i, e := range es {
			out[i] = e.link
		}
		return out
	}
	fresh, rest := links(freshE), links(restE)

	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	m := meta{Updated: time.Now().UTC(), Sources: len(srcs), Reached: reached, Found: len(byKey), Dead: dead, Tested: len(tested), Passed: len(passed), Files: map[string]int{},
		Schemes: schemes, Protocols: protocols, Channels: channels}
	write := func(name string, links []string) {
		if err := os.WriteFile(filepath.Join(*out, name), []byte(strings.Join(links, "\n")+"\n"), 0o644); err != nil {
			log.Fatal(err)
		}
		m.Files[name] = len(links)
	}
	write("fresh.txt", fresh)
	for i := range *shards {
		lo, hi := len(rest)*i / *shards, len(rest)*(i+1) / *shards
		write(fmt.Sprintf("all-%d.txt", i+1), rest[lo:hi])
	}
	// The project's own servers (deploy/server) go out sealed, so public
	// collectors do not pick them up and overload them.
	if own, key := os.Getenv("OWN_SERVERS"), os.Getenv("OWN_KEY"); own != "" && key != "" {
		k, err := hex.DecodeString(key)
		if err != nil || len(k) != 32 {
			log.Fatal("OWN_KEY must be 64 hex digits")
		}
		// Split into sources.OwnBuckets parts; each app reads two. With
		// fewer servers than parts, each part gets one of them.
		links := strings.Fields(own)
		for b := range sources.OwnBuckets {
			var part []string
			for i, l := range links {
				if i%sources.OwnBuckets == b {
					part = append(part, l)
				}
			}
			if len(part) == 0 {
				part = []string{links[b%len(links)]}
			}
			sealed, err := sources.Seal(k, strings.Join(part, "\n")+"\n")
			if err != nil {
				log.Fatal(err)
			}
			name := fmt.Sprintf("own-%d.bin", b+1)
			if err := os.WriteFile(filepath.Join(*out, name), sealed, 0o644); err != nil {
				log.Fatal(err)
			}
			m.Files[name] = len(part)
		}
	}
	if br, err := torBridges(ctx, &http.Client{Timeout: 90 * time.Second}); err != nil {
		log.Printf("tor bridges: %v", err)
	} else {
		b, _ := json.Marshal(br)
		if err := os.WriteFile(filepath.Join(*out, "tor.json"), b, 0o644); err != nil {
			log.Fatal(err)
		}
		for _, v := range br {
			m.Files["tor.json"] += len(v)
		}
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(*out, "meta.json"), b, 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %d fresh and %d other servers to %s", len(fresh), len(rest), *out)
}

// probeAll opens a TCP connection to every distinct address once.
func probeAll(list []*entry, conc int) map[string]bool {
	seen := map[string]bool{}
	var eps []string
	for _, e := range list {
		if e.endpoint != "" && !seen[e.endpoint] {
			seen[e.endpoint] = true
			eps = append(eps, e.endpoint)
		}
	}
	log.Printf("probing %d addresses", len(eps))
	alive := map[string]bool{}
	var mu sync.Mutex
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	d := net.Dialer{Timeout: 4 * time.Second}
	for _, ep := range eps {
		wg.Add(1)
		sem <- struct{}{}
		go func(ep string) {
			defer wg.Done()
			defer func() { <-sem }()
			for range 2 {
				c, err := d.Dial("tcp", ep)
				if err == nil {
					c.Close()
					mu.Lock()
					alive[ep] = true
					mu.Unlock()
					return
				}
			}
		}(ep)
	}
	wg.Wait()
	return alive
}

// flagOf turns a country code into its flag emoji.
func flagOf(code string) string {
	if len(code) != 2 {
		return ""
	}
	code = strings.ToUpper(code)
	if code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z' {
		return ""
	}
	return string([]rune{0x1F1E6 + rune(code[0]-'A'), 0x1F1E6 + rune(code[1]-'A')})
}
