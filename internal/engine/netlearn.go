package engine

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cezomagoanlace29861-jpg/vp/internal/node"
)

// netLearn keeps track of which protocols and wire styles (see node.Style)
// pass, separately for each network the device uses (told apart by the
// network's resolver: each operator and each home router has its own).
// Filtering differs between operators and over time: one day only Reality
// gets through, another day WebSocket over TLS does. Tests of the kinds
// that work are moved to the front, so a working server is found sooner
// without the user picking anything.
type netLearn struct {
	mu   sync.Mutex
	Nets map[string]*netTable `json:"nets"`
	cur  string
}

type netTable struct {
	Proto [16]passRate `json:"proto"`
	Style [32]passRate `json:"style"`
	// Hello is per ClientHello (helloCode: browser fingerprint and
	// fragmentation) on TLS and Reality servers.
	Hello [16]passRate `json:"hello"`
	// Port is per port class (node.StylePort).
	Port [8]passRate `json:"port"`
	// Life is how long a server of each wire style (node.StyleWire) lasts
	// in use here before filtering cuts it.
	Life [32]lifeRate `json:"life"`
	All  passRate     `json:"all"`
	Used time.Time    `json:"used"`
	// Pend counts tests since the last crowd report (crowd.go); ASN is the
	// network's operator as the crowd service saw it, and Seeded says the
	// crowd's numbers were already added to this table.
	Pend   crowdCounts `json:"pend"`
	ASN    uint32      `json:"asn,omitempty"`
	Seeded bool        `json:"seeded,omitempty"`
}

// lifeRate is a running average of lifetimes in minutes over N deaths.
type lifeRate struct {
	Min float64 `json:"min"`
	N   float64 `json:"n"`
}

type passRate struct {
	OK float64 `json:"ok"`
	N  float64 `json:"n"`
}

// Old results fade: each new test weighs the past down a little, so the
// last few thousand tests decide.
const learnDecay = 0.9995

// maxNets bounds how many networks are remembered.
const maxNets = 16

// table returns the current network's table. Callers hold l.mu.
func (l *netLearn) table() *netTable {
	if l.Nets == nil {
		l.Nets = map[string]*netTable{}
	}
	t := l.Nets[l.cur]
	if t == nil {
		t = &netTable{}
		l.Nets[l.cur] = t
	}
	return t
}

// use switches to the network whose resolver is key. A network seen for the
// first time starts from a fifth of what the last one taught: filtering is
// national as much as it is per operator.
func (l *netLearn) use(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if key == l.cur {
		return
	}
	prev := l.table()
	l.cur = key
	if _, ok := l.Nets[key]; ok {
		return
	}
	t := *prev
	t.scale(0.2)
	t.Pend, t.ASN, t.Seeded = crowdCounts{}, 0, false
	l.Nets[key] = &t
	for len(l.Nets) > maxNets {
		oldest := ""
		for k, n := range l.Nets {
			if k != l.cur && (oldest == "" || n.Used.Before(l.Nets[oldest].Used)) {
				oldest = k
			}
		}
		delete(l.Nets, oldest)
	}
}

func (t *netTable) scale(f float64) {
	sc := func(r *passRate) { r.OK *= f; r.N *= f }
	sc(&t.All)
	for i := range t.Proto {
		sc(&t.Proto[i])
	}
	for i := range t.Style {
		sc(&t.Style[i])
	}
	for i := range t.Hello {
		sc(&t.Hello[i])
	}
	for i := range t.Port {
		sc(&t.Port[i])
	}
	for i := range t.Life {
		t.Life[i].N *= f
	}
}

func (r *passRate) add(ok bool) {
	r.OK *= learnDecay
	r.N *= learnDecay
	r.N++
	if ok {
		r.OK++
	}
}

// recordHello counts one test of a ClientHello (see helloCode).
func (l *netLearn) recordHello(code int, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.table()
	if code >= 0 && code < len(t.Hello) {
		t.Hello[code].add(ok)
		t.Pend.Hello[code].add(ok)
	}
}

func (l *netLearn) record(proto, style uint8, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.table()
	t.Used = time.Now()
	add := func(r *passRate) { r.add(ok) }
	add(&t.All)
	if int(proto) < len(t.Proto) {
		add(&t.Proto[proto])
		t.Pend.Proto[proto].add(ok)
	}
	add(&t.Style[node.StyleWire(style)])
	t.Pend.Style[node.StyleWire(style)].add(ok)
	if p := node.StylePort(style); p != 0 && int(p) < len(t.Port) {
		add(&t.Port[p])
		t.Pend.Port[p].add(ok)
	}
}

// learnedView is a copy for scoring without holding the lock.
type learnedView struct {
	proto [16]float64
	style [32]float64
	hello [16]float64
	port  [8]float64
}

func (l *netLearn) snapshot() learnedView {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.table()
	var v learnedView
	base := (t.All.OK + 1) / (t.All.N + 2)
	// A kind with few tests counts as average until it has more.
	const k = 30
	lift := func(r passRate) float64 {
		got := (r.OK + k*base) / (r.N + k)
		return math.Max(-75, math.Min(75, 25*math.Log2(got/base)))
	}
	for i := range t.Proto {
		v.proto[i] = lift(t.Proto[i])
	}
	for i := range t.Style {
		v.style[i] = lift(t.Style[i])
	}
	for i := range t.Hello {
		v.hello[i] = lift(t.Hello[i])
	}
	for i := range t.Port {
		v.port[i] = lift(t.Port[i])
	}
	return v
}

func (v learnedView) bonus(proto, style uint8) float64 {
	b := 0.0
	if int(proto) < len(v.proto) {
		b += v.proto[proto]
	}
	b += v.style[node.StyleWire(style)]
	if p := node.StylePort(style); p != 0 {
		b += v.port[p]
	}
	return b
}

// recordLife counts a server of this style that died after d in use.
// Deaths in the first half minute are failures, not lifetimes.
func (l *netLearn) recordLife(style uint8, d time.Duration) {
	if d < 30*time.Second || d > 6*time.Hour {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	r := &l.table().Life[node.StyleWire(style)]
	// The newest deaths count most: filtering rules change.
	w := 1 / min(r.N+1, 8)
	r.Min = r.Min*(1-w) + d.Minutes()*w
	r.N++
}

// life is how long a server of this style usually lasts here, if known.
func (l *netLearn) life(style uint8) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.table().Life[node.StyleWire(style)]
	if r.N < 3 {
		return 0, false
	}
	return time.Duration(r.Min * float64(time.Minute)), true
}

func (l *netLearn) load(dir string) {
	b, err := os.ReadFile(filepath.Join(dir, "netlearn.json"))
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	json.Unmarshal(b, l)
}

func (l *netLearn) save(dir string) {
	l.mu.Lock()
	b, err := json.Marshal(l)
	l.mu.Unlock()
	if err != nil {
		return
	}
	path := filepath.Join(dir, "netlearn.json")
	if os.WriteFile(path+".tmp", b, 0o644) == nil {
		os.Rename(path+".tmp", path)
	}
}
