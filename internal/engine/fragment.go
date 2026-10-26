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
	// Three to five pieces, the first ones small: the name sits a little
	// way into the hello, after the random and the session id.
	n := 3 + rand.Intn(3)
	cuts := make([]int, 0, n)
	for i := 1; i < n; i++ {
		cuts = append(cuts, 1+rand.Intn(len(b)-1))
	}
	cuts = append(cuts, len(b))
	sortInts(cuts)
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

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}
