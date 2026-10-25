package engine

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Albatrossvpn/vp/internal/antiblock"
	"github.com/Albatrossvpn/vp/internal/node"
	"github.com/Albatrossvpn/vp/internal/pool"
)

// Server is a server that has worked at least once.
type Server struct {
	Key       string    `json:"key"`
	Link      string    `json:"link"`
	Name      string    `json:"name"`
	Proto     string    `json:"proto"`
	Transport string    `json:"transport,omitempty"`
	Security  string    `json:"security,omitempty"`
	Engine    string    `json:"engine"`
	Country   string    `json:"country"`
	ExitIP    string    `json:"exit_ip"`
	Delay     int       `json:"delay"` // milliseconds, last successful test
	TestedAt  time.Time `json:"tested_at"`
	LastOK    time.Time `json:"last_ok"`
	Fails     int       `json:"fails"` // consecutive failures since LastOK
	OKCount   int       `json:"ok_count"`
	Fragment  bool      `json:"fragment,omitempty"`   // only works with TLS fragmentation
	Hello     string    `json:"hello,omitempty"`      // browser ClientHello the app picked
	ECH       bool      `json:"ech,omitempty"`        // only works with ECH looked up over DNS
	PinIP     string    `json:"pin_ip,omitempty"`     // connect here instead of resolving the name
	SplitIP   string    `json:"split_ip,omitempty"`   // XHTTP downloads through this edge
	PinReason string    `json:"pin_reason,omitempty"` // "dns" (poisoned) or "clean" (Cloudflare edge)
	Plain     bool      `json:"plain,omitempty"`
	Source    string    `json:"source,omitempty"`
	// Domestic says the server's address is inside the country (a relay);
	// Own that it came from the project's own sealed lists.
	Domestic bool `json:"domestic,omitempty"`
	Own      bool `json:"own,omitempty"`

	key64 uint64
	node  *node.Node
}

// Node parses the server's link once and caches it.
func (s *Server) Node() *node.Node {
	if s.node == nil {
		n, err := node.ParseLink(s.Link)
		if err != nil {
			return nil
		}
		s.node = n
	}
	return s.node
}

// runNode is the node as it must run: pinned to a working IP when the
// name is poisoned or a clean Cloudflare edge was needed.
func (s *Server) runNode() *node.Node {
	n := s.Node()
	if n != nil && (s.Hello != "" && helloable(n) || s.ECH) {
		c := *n
		if helloable(n) {
			c.HelloFP = s.Hello
		}
		c.TryECH = s.ECH
		n = &c
	}
	if n == nil || s.PinIP == "" {
		return n
	}
	n = antiblock.Pin(n, s.PinIP)
	n.SplitDownload = s.SplitIP
	return n
}

// matches reports whether the server uses the chosen protocol ("" = any).
func (s *Server) matches(protocol string) bool { return protocol == "" || s.Proto == protocol }

// usable reports whether the server is worth connecting to now.
func (s *Server) usable(now time.Time) bool {
	return s.Fails < 3 && now.Sub(s.LastOK) < 6*time.Hour
}

// rank orders servers for connecting: low delay, recently confirmed and
// rarely failing first.
func (s *Server) rank(now time.Time) float64 {
	r := float64(s.Delay)
	age := now.Sub(s.LastOK)
	switch {
	case age > 2*time.Hour:
		r += 1500
	case age > 30*time.Minute:
		r += 400
	}
	r += float64(s.Fails) * 800
	if s.Fragment {
		r += 150
	}
	return r
}

// nationalCapable reports whether the server can work where only domestic
// addresses answer, by what the regime probe measured: a relay inside the
// country, the DNS tunnel through a resolver that still recurses, the ping
// carrier while ping gets out, UDP 53 while it does.
func (s *Server) nationalCapable(p regimeProbe) bool {
	if s.Domestic {
		return true
	}
	n := s.Node()
	if n == nil {
		return false
	}
	switch {
	case n.DNSTunnel():
		return p.Recursion
	case n.ICMP():
		return p.ICMP != "no"
	case n.Port == 53 && n.UDPBased():
		return p.UDP53
	}
	return false
}

// rankIn is rank with the regime in mind: where foreign addresses are cut
// or nearly so, the servers that can still get out come far ahead.
func (s *Server) rankIn(now time.Time, r regime, p regimeProbe) float64 {
	v := s.rank(now)
	if r.severity() >= regimeAllowlist.severity() {
		if s.nationalCapable(p) {
			v -= 3000
		} else if r == regimeNational {
			v += 2000
		}
	}
	return v
}

func (e *Engine) loadServers() {
	b, err := os.ReadFile(filepath.Join(e.dir, "servers.json"))
	if err != nil {
		return
	}
	var list []*Server
	if json.Unmarshal(b, &list) != nil {
		return
	}
	now := time.Now()
	for _, s := range list {
		if now.Sub(s.LastOK) > 7*24*time.Hour {
			continue
		}
		s.key64 = node.Key64(s.Key)
		e.good[s.key64] = s
	}
}

func (e *Engine) saveServers() {
	e.mu.Lock()
	list := make([]*Server, 0, len(e.good))
	for _, s := range e.good {
		c := *s
		c.node = nil
		list = append(list, &c)
	}
	e.mu.Unlock()
	saveJSON(filepath.Join(e.dir, "servers.json"), list)
	e.net.save(e.dir)
}

// SourceStat tracks how useful a source has been.
type SourceStat struct {
	Tested    int       `json:"tested"`
	OK        int       `json:"ok"`
	Nodes     int       `json:"nodes"`
	Fetched   time.Time `json:"fetched"`
	LastError string    `json:"last_error,omitempty"`
}

// score estimates the chance a fresh node from this source works, with a
// prior so unknown sources still get tried.
func (st *SourceStat) score() float64 {
	return (float64(st.OK) + 1) / (float64(st.Tested) + 12)
}

func (e *Engine) loadSourceStats() {
	b, err := os.ReadFile(filepath.Join(e.dir, "sources.json"))
	if err != nil {
		return
	}
	json.Unmarshal(b, &e.srcStatsByID)
}

func (e *Engine) saveSourceStats() {
	e.mu.Lock()
	m := map[string]SourceStat{}
	for i, s := range e.srcs {
		m[s.ID] = e.srcStats[i]
	}
	e.mu.Unlock()
	saveJSON(filepath.Join(e.dir, "sources.json"), m)
}

// Test states are kept across restarts so dead servers are not retested
// right away. Each record is key(8) lastTest(4) state(1) fails(1).
const stateRecord = 14

func (e *Engine) saveStates() {
	path := filepath.Join(e.dir, "states.bin")
	f, err := os.Create(path + ".tmp")
	if err != nil {
		return
	}
	w := bufio.NewWriterSize(f, 1<<16)
	var rec [stateRecord]byte
	e.pool.Each(func(en *pool.Entry) {
		if en.State == pool.StateNew {
			return
		}
		binary.LittleEndian.PutUint64(rec[0:], en.Key)
		binary.LittleEndian.PutUint32(rec[8:], en.LastTest)
		rec[12] = en.State
		rec[13] = en.Fails
		w.Write(rec[:])
	})
	w.Flush()
	f.Close()
	os.Rename(path+".tmp", path)
}

func (e *Engine) loadStates() map[uint64][2]uint32 {
	out := map[uint64][2]uint32{}
	f, err := os.Open(filepath.Join(e.dir, "states.bin"))
	if err != nil {
		return out
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<16)
	var rec [stateRecord]byte
	cutoff := pool.Minutes(time.Now().Add(-72 * time.Hour))
	for {
		if _, err := io.ReadFull(r, rec[:]); err != nil {
			break
		}
		last := binary.LittleEndian.Uint32(rec[8:])
		// Rejected servers are remembered for good (pool.StateRejected).
		if last < cutoff && rec[12] != pool.StateRejected {
			continue
		}
		out[binary.LittleEndian.Uint64(rec[0:])] = [2]uint32{last, uint32(rec[12]) | uint32(rec[13])<<8}
	}
	return out
}
