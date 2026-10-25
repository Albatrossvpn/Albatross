package engine

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"errors"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/cezomagoanlace29861-jpg/vp/internal/geo"
	"github.com/cezomagoanlace29861-jpg/vp/internal/node"
	"github.com/cezomagoanlace29861-jpg/vp/internal/pool"
	"github.com/cezomagoanlace29861-jpg/vp/internal/sources"
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
)

// ShareCode returns a code holding the servers that work best now.
func (e *Engine) ShareCode() (string, error) {
	now := time.Now()
	e.mu.Lock()
	var list []*Server
	for _, s := range e.good {
		if s.usable(now) && s.Source != "warp-plus" && s.Source != "usque" && s.Source != "tor" && s.Source != "direct" {
			list = append(list, s)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].rank(now) < list[j].rank(now) })
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
	node.EachText(string(b), func(n *node.Node) {
		eng := n.Engine()
		if eng == node.EngineNone {
			return
		}
		en := pool.Entry{Key: node.Key64(n.Key()), Source: uint16(src), Proto: protoCodes[n.Proto], Engine: uint8(eng),
			Style: n.Style(), Flags: pool.FlagFresh, Pop: 10}
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
