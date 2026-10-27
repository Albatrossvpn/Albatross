package engine

import (
	"math/rand"
	"net"
	"time"
)

// fragConn sends the first thing written on a connection, the TLS
// ClientHello, as several TCP segments with a pause between them. The
// filtering here reads the server name from the first segment of a flow;
// a name split over segments goes unread, while the server, which
// reassembles the stream, sees it whole.
type fragConn struct {
	net.Conn
	done bool
}

// fragPause is the wait between the pieces, long enough for them to leave
// as separate segments.
var fragPause = 20 * time.Millisecond

func (c *fragConn) Write(b []byte) (int, error) {
	if c.done || len(b) < 16 {
		return c.Conn.Write(b)
	}
	c.done = true
	cuts := helloCuts(b)
	sent, from := 0, 0
	for _, to := range cuts {
		if to <= from {
			continue
		}
		k, err := c.Conn.Write(b[from:to])
		sent += k
		if err != nil {
			return sent, err
		}
		from = to
		if to < len(b) {
			time.Sleep(fragPause)
		}
	}
	return sent, nil
}

// helloCuts says where the ClientHello b is cut: the ends of three to six
// pieces, the last one len(b). The first piece ends inside the record and
// handshake headers (the 43 bytes before the session id), so the first
// segment never holds the name; the next ends inside the name itself, so a
// filter that joins two segments still sees it cut; the rest fall anywhere.
func helloCuts(b []byte) []int {
	size := len(b)
	cuts := []int{1 + rand.Intn(min(40, size-1))}
	if from, to, ok := sniSpan(b); ok && to-from > 1 && from+1 > cuts[0] {
		cuts = append(cuts, from+1+rand.Intn(to-from-1))
	} else if hi := min(320, size-1); hi > cuts[0] {
		// No name found: where it usually is, 100 to 300 bytes in.
		cuts = append(cuts, cuts[0]+1+rand.Intn(hi-cuts[0]))
	}
	for extra := 1 + rand.Intn(3); extra > 0; extra-- {
		cuts = append(cuts, 1+rand.Intn(size-1))
	}
	cuts = append(cuts, size)
	sortInts(cuts)
	return cuts
}

// sniSpan finds the server name in a TLS record holding a ClientHello: the
// offsets of its first byte and of the byte after it.
func sniSpan(b []byte) (from, to int, ok bool) {
	u16 := func(i int) int { return int(b[i])<<8 | int(b[i+1]) }
	// Record header (5), handshake header (4), version (2), random (32).
	p := 5 + 4 + 2 + 32
	if len(b) < p+1 || b[0] != 22 || b[5] != 1 {
		return 0, 0, false
	}
	p += 1 + int(b[p]) // session id
	if len(b) < p+2 {
		return 0, 0, false
	}
	p += 2 + u16(p) // cipher suites
	if len(b) < p+1 {
		return 0, 0, false
	}
	p += 1 + int(b[p]) // compression methods
	if len(b) < p+2 {
		return 0, 0, false
	}
	end := min(len(b), p+2+u16(p))
	for p += 2; p+4 <= end; {
		typ, n := u16(p), u16(p+2)
		p += 4
		if typ == 0 && p+5 <= end { // server_name: list length, name type, name length
			from, to = p+5, p+5+u16(p+3)
			return from, to, to <= end
		}
		p += n
	}
	return 0, 0, false
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}
