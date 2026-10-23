package engine

import "github.com/cezomagoanlace29861-jpg/vp/internal/node"

// helloFPs are the browser ClientHellos tried on TLS and Reality servers
// whose link names none. Filtering tells them apart: Chrome's current hello
// carries a post-quantum key share that makes it span two TCP segments,
// which some networks drop, while Edge's and Safari's are smaller. Which
// one gets through, with or without fragmentation, is learned per network.
var helloFPs = []string{"chrome", "edge", "safari", "firefox"}

// helloExplore is how often a test tries a hello other than the best
// known one, so the app notices when filtering changes.
const helloExplore = 0.2

// helloCode numbers a hello for the learning table.
func helloCode(fp int, frag bool) int {
	c := fp * 2
	if frag {
		c++
	}
	return c
}

func helloIndex(fp string) int {
	for i, f := range helloFPs {
		if f == fp {
			return i
		}
	}
	return -1
}

// helloable reports whether the app may pick n's ClientHello.
func helloable(n *node.Node) bool {
	return n.Fingerprint == "" && (n.Security == "tls" || n.Security == "reality") && !n.UDPBased() && n.Proto != "warpplus"
}

// pickHello returns the hello to test with: usually the best on this
// network, sometimes another.
func (v learnedView) pickHello(frag bool, rnd func() float64) int {
	if rnd() < helloExplore {
		return int(rnd() * float64(len(helloFPs)) * 0.9999)
	}
	return v.nextHello(-1, frag)
}

// nextHello is the best hello other than skip.
func (v learnedView) nextHello(skip int, frag bool) int {
	best := -1
	for i := range helloFPs {
		if i != skip && (best < 0 || v.hello[helloCode(i, frag)] > v.hello[helloCode(best, frag)]) {
			best = i
		}
	}
	return best
}

// fragHelps reports whether fragmenting this hello does better than not
// here, and fragHurts whether it is clearly worse (some networks drop
// fragmented hellos outright).
func (v learnedView) fragHelps(fp int) bool {
	return v.hello[helloCode(fp, true)] > v.hello[helloCode(fp, false)]+15
}
func (v learnedView) fragHurts(fp int) bool {
	return v.hello[helloCode(fp, true)] < v.hello[helloCode(fp, false)]-20
}

// setHello makes c use hello fp, on a copy of its node.
func (c *candidate) setHello(fp int) {
	n := *c.node
	n.HelloFP = helloFPs[fp]
	c.node, c.hello = &n, int8(fp+1)
}

// fp is the index of the hello the app picked for c, or -1.
func (c candidate) fp() int { return int(c.hello) - 1 }

func (c candidate) helloCode() int {
	if c.hello == 0 {
		return -1
	}
	return helloCode(c.fp(), c.frag)
}
