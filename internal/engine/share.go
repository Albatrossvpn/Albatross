package engine

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Albatrossvpn/vp/internal/geo"
	"github.com/Albatrossvpn/vp/internal/node"
	"github.com/Albatrossvpn/vp/internal/pool"
	"github.com/Albatrossvpn/vp/internal/sources"
)

// Passing servers from hand to hand. In a full shutdown no list reaches the
// device, but a friend's phone may still hold servers that work; a share
// code carries the best of them over any channel that still works
// (Bluetooth, Quick Share, a domestic messenger, a QR photo). The code is
// sealed, so the links stay hidden like everywhere else in the app and a
// damaged code is refused.

const (
	sharePrefix = "albatross1:"
	shareMax    = 40
	// shareMirror starts a line that carries a mirror inside the country
	// (sources.DomesticBases) instead of a server: during a shutdown a
	// friend's phone passes on where the lists still come from, too.
	shareMirror    = "#mirror "
	shareMirrorMax = 8
)

// ShareCode returns a code holding the servers that work best now.
func (e *Engine) ShareCode() (string, error) {
	now := time.Now()
	e.mu.Lock()
	reg, p := e.reg.Regime, e.reg.Probe
	// The project's own servers stay with the plans that have them; the
	// relays inside the country go, in a shutdown they are what matters.
	// The ones made from this device's WARP account carry its key: one
	// account on two devices breaks both.
	warp := sources.Warp().Label()
	own := map[string]bool{}
	for _, s := range e.srcs {
		if ownSource(s) {
			own[s.Label()] = true
		}
	}
	var list []*Server
	for _, s := range e.good {
		if s.usable(now) && s.Source != "warp-plus" && s.Source != "usque" && s.Source != "tor" && s.Source != "direct" && s.Source != "edge" && s.Source != warp && !(s.Own && own[s.Source]) {
			list = append(list, s)
		}
	}
	// Where foreign addresses are cut, the servers that still get out
	// (relays inside the country, the DNS and ping carriers) go first:
	// they are what the friend's phone needs most.
	sort.Slice(list, func(i, j int) bool { return list[i].rankIn(now, reg, p) < list[j].rankIn(now, reg, p) })
	var links []string
	for _, s := range list {
		if len(links) == shareMax {
			break
		}
		link := s.Link
		if s.Country != "" && geo.Hint(s.Name) == "" {
			// Keep the location the receiving app would otherwise relearn.
			link = withFlag(link, s.Country)
		}
		links = append(links, link)
	}
	e.mu.Unlock()
	if len(links) == 0 {
		return "", errors.New("no working servers to share yet")
	}
	for i, b := range sources.DomesticBases() {
		if i == shareMirrorMax {
			break
		}
		links = append(links, shareMirror+b)
	}
	var z bytes.Buffer
	w, _ := gzip.NewWriterLevel(&z, gzip.BestCompression)
	w.Write([]byte(strings.Join(links, "\n")))
	w.Close()
	sealed, err := sources.Seal(sources.ShareKey(), z.String())
	if err != nil {
		return "", err
	}
	return sharePrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

// withFlag puts a country's flag in front of a link's name when the link
// format keeps the name in its fragment.
func withFlag(link, country string) string {
	i := strings.LastIndexByte(link, '#')
	if i < 0 || strings.HasPrefix(link, "vmess://") {
		return link
	}
	return link[:i+1] + geo.Flag(country) + "%20" + link[i+1:]
}

// ImportShareCode adds the servers in a share code and tests them first.
func (e *Engine) ImportShareCode(code string) (int, error) {
	code = strings.TrimSpace(code)
	i := strings.Index(code, sharePrefix)
	if i < 0 {
		return 0, errors.New("this is not a server share code")
	}
	code = strings.Fields(code[i+len(sharePrefix):] + " ")[0]
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(code, "="))
	if err != nil {
		return 0, errors.New("the share code is damaged")
	}
	z := sources.OpenWith(sources.ShareKey(), string(raw))
	if z == "" {
		return 0, errors.New("the share code is damaged or from another app")
	}
	r, err := gzip.NewReader(strings.NewReader(z))
	if err != nil {
		return 0, errors.New("the share code is damaged")
	}
	b, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil {
		return 0, errors.New("the share code is damaged")
	}
	text, mirrors := splitShareMirrors(string(b))
	if len(mirrors) > 0 {
		e.addDomesticMirrors(mirrors)
	}
	e.mu.Lock()
	src := 0
	for i, s := range e.srcs {
		if s.Central && s.Tier == sources.TierPriority {
			src = i
			break
		}
	}
	e.mu.Unlock()
	added := 0
	node.EachText(text, func(n *node.Node) {
		eng := n.Engine()
		if eng == node.EngineNone {
			return
		}
		en := pool.Entry{Key: node.Key64(n.Key()), Source: uint16(src), Proto: protoCodes[n.Proto], Engine: uint8(eng),
			Style: n.Style(), Net: n.NetID(), Flags: pool.FlagFresh, Pop: 10}
		if h := geo.Hint(n.Name); len(h) == 2 {
			en.Hint = [2]byte{h[0], h[1]}
		}
		if n.UDPBased() {
			en.Flags |= pool.FlagUDP
		}
		if isPlain(n) {
			en.Flags |= pool.FlagPlain
		}
		if e.pool.Add(en, n.Link(), func(uint16) float64 { return 100 }) {
			added++
		} else {
			// Known already, and likely marked dead while everything was
			// failing: a friend says it works, so it is tried again now. A
			// server working here already stays as it is.
			e.pool.Update(en.Key, func(p *pool.Entry) {
				if p.State == pool.StateDead {
					p.State, p.Fails, p.LastTest = pool.StateNew, 0, 0
					p.Flags |= pool.FlagFresh
					added++
				}
			})
		}
	})
	if added > 0 {
		e.logf("info", "%d shared servers added; testing them now.", added)
		select {
		case e.wake <- struct{}{}:
		default:
		}
	}
	return added, nil
}

// splitShareMirrors takes the mirror lines out of a share code's text and
// returns the servers' text and the mirrors that pass validMirror.
func splitShareMirrors(text string) (string, []string) {
	var keep, mirrors []string
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, shareMirror) {
			keep = append(keep, line)
			continue
		}
		if m := strings.TrimRight(strings.TrimSpace(line[len(shareMirror):]), "/"); validMirror(m) && len(mirrors) < shareMirrorMax {
			mirrors = append(mirrors, m)
		}
	}
	return strings.Join(keep, "\n"), mirrors
}

// validMirror accepts a mirror base a share code may carry: plain http or
// https to an address inside the country (an IP, or a name under .ir),
// with at most a path. A sealed list is what it serves, so a wrong mirror
// can waste a request but not plant servers.
func validMirror(m string) bool {
	u, err := url.Parse(m)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	h := u.Hostname()
	if h == "" || len(m) > 200 || strings.HasSuffix(m, "/") {
		return false
	}
	if ip := net.ParseIP(h); ip != nil {
		return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsUnspecified()
	}
	return strings.HasSuffix(strings.ToLower(h), ".ir")
}

// addDomesticMirrors remembers mirrors a share code brought (newest
// first, shareMirrorMax at most) and fetches the lists through them.
func (e *Engine) addDomesticMirrors(mirrors []string) {
	e.mu.Lock()
	seen := map[string]bool{}
	var list []string
	for _, m := range append(append([]string(nil), mirrors...), e.settings.DomesticMirrors...) {
		if m = strings.TrimRight(m, "/"); !seen[m] && len(list) < shareMirrorMax {
			seen[m] = true
			list = append(list, m)
		}
	}
	changed := strings.Join(list, " ") != strings.Join(e.settings.DomesticMirrors, " ")
	e.settings.DomesticMirrors = list
	e.mu.Unlock()
	if !changed {
		return
	}
	saveJSON(filepath.Join(e.dir, "settings.json"), e.Settings())
	e.logf("info", "%d shared mirrors inside the country added.", len(mirrors))
	e.triggerRefill()
}
