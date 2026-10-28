package engine

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/url"
	"strconv"
	"strings"
)

// The servers of the device's own WARP account (sources.Warp). Most of the
// protocols the app offers have next to no public servers; WARP's
// endpoints take any account's WireGuard, and AmneziaWG's too: its junk
// packets before the handshake, which hide WireGuard's look from a filter,
// are dropped by WARP unread, since the message types stay WireGuard's (no
// H1 to H4) and the handshakes are not padded (no S1, S2).

// warpAddrs are WARP's endpoint addresses: each answers any account, and a
// filter that blocks some may leave others.
var warpAddrs = []string{"162.159.192.1", "162.159.193.1", "162.159.195.1", "188.114.96.1", "188.114.97.1", "188.114.98.1", "188.114.99.1"}

// warpPorts are ports WARP's endpoints answer on.
var warpPorts = []int{2408, 500, 1701, 4500, 854, 878, 894, 908, 942, 1180, 2371, 4198, 5279, 7559, 8854}

// warpJunk are the junk packets to try before the handshake: how many,
// and their smallest and largest sizes.
var warpJunk = [][3]int{{4, 40, 70}, {8, 10, 50}, {16, 64, 128}}

// warpLinks makes the links of sources.Warp from the device's WARP
// account, signing one up first (through the VPN when connected).
func (e *Engine) warpLinks(ctx context.Context) (string, error) {
	w := e.warpExit()
	if w == nil {
		if e.opt.Version == "test" {
			return "", errors.New("test runs sign no device up")
		}
		client := e.proxyClient()
		if client == nil {
			client = e.fetcher.Direct
		}
		if err := e.registerWarp(ctx, client); err != nil {
			return "", err
		}
		if w = e.warpExit(); w == nil {
			return "", errors.New("the WARP sign-up was not saved")
		}
	}
	return w.links(), nil
}

// links lists AmneziaWG and WireGuard to a spread of WARP's addresses and
// ports. Each device starts the spread elsewhere (from its key), so the
// devices behind one filter do not all knock on the same endpoint.
func (w *warpID) links() string {
	h := fnv.New32a()
	io.WriteString(h, w.PrivateKey)
	seed := int(h.Sum32() % 4096)
	addr := w.V4 + "/32"
	if w.V6 != "" {
		addr += "," + w.V6 + "/128"
	}
	priv, peer, address := url.QueryEscape(w.PrivateKey), url.QueryEscape(w.PeerKey), url.QueryEscape(addr)
	name := func(kind, ip string, port int) string {
		return url.PathEscape("WARP " + kind + " " + ip + ":" + strconv.Itoa(port))
	}
	var b strings.Builder
	for i := 0; i < 12; i++ {
		ip, port, j := warpAddrs[(seed+i)%len(warpAddrs)], warpPorts[(seed/7+i*4)%len(warpPorts)], warpJunk[i%len(warpJunk)]
		fmt.Fprintf(&b, "amneziawg://%s@%s:%d?publickey=%s&address=%s&mtu=1280&jc=%d&jmin=%d&jmax=%d#%s\n",
			priv, ip, port, peer, address, j[0], j[1], j[2], name("AmneziaWG", ip, port))
	}
	reserved := ""
	if len(w.Reserved) == 3 {
		reserved = fmt.Sprintf("&reserved=%d,%d,%d", w.Reserved[0], w.Reserved[1], w.Reserved[2])
	}
	for i := 0; i < 6; i++ {
		ip, port := warpAddrs[(seed+3+i)%len(warpAddrs)], warpPorts[(seed/3+i*5)%len(warpPorts)]
		fmt.Fprintf(&b, "wireguard://%s@%s:%d?publickey=%s&address=%s&mtu=1280%s#%s\n",
			priv, ip, port, peer, address, reserved, name("WireGuard", ip, port))
	}
	return b.String()
}
