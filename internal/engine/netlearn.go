package engine

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sync"
)

// netLearn keeps track of which protocols and wire styles (see node.Style)
// pass on the current network. Filtering differs between networks and over
// time: one day only Reality gets through, another day WebSocket over TLS
// does. Tests of the kinds that work are moved to the front, so a working
// server is found sooner without the user picking anything.
type netLearn struct {
	mu    sync.Mutex
	Proto [16]passRate `json:"proto"`
	Style [32]passRate `json:"style"`
	All   passRate     `json:"all"`
}

type passRate struct {
	OK float64 `json:"ok"`
	N  float64 `json:"n"`
}

// Old results fade: each new test weighs the past down a little, so the
// last few thousand tests decide.
const learnDecay = 0.9995

func (l *netLearn) record(proto, style uint8, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	add := func(r *passRate) {
		r.OK *= learnDecay
		r.N *= learnDecay
		r.N++
		if ok {
			r.OK++
		}
	}
	add(&l.All)
	if int(proto) < len(l.Proto) {
		add(&l.Proto[proto])
	}
	if int(style) < len(l.Style) {
		add(&l.Style[style])
	}
}

// fade keeps only a fifth of what was learned, for when the device moved
// to another network.
func (l *netLearn) fade() {
	l.mu.Lock()
	defer l.mu.Unlock()
	scale := func(r *passRate) { r.OK *= 0.2; r.N *= 0.2 }
	scale(&l.All)
	for i := range l.Proto {
		scale(&l.Proto[i])
	}
	for i := range l.Style {
		scale(&l.Style[i])
	}
}

// learnedView is a copy for scoring without holding the lock.
type learnedView struct {
	proto [16]float64
	style [32]float64
}

func (l *netLearn) snapshot() learnedView {
	l.mu.Lock()
	defer l.mu.Unlock()
	var v learnedView
	base := (l.All.OK + 1) / (l.All.N + 2)
	// A kind with few tests counts as average until it has more.
	const k = 30
	lift := func(r passRate) float64 {
		got := (r.OK + k*base) / (r.N + k)
		return math.Max(-75, math.Min(75, 25*math.Log2(got/base)))
	}
	for i := range l.Proto {
		v.proto[i] = lift(l.Proto[i])
	}
	for i := range l.Style {
		v.style[i] = lift(l.Style[i])
	}
	return v
}

func (v learnedView) bonus(proto, style uint8) float64 {
	b := 0.0
	if int(proto) < len(v.proto) {
		b += v.proto[proto]
	}
	if int(style) < len(v.style) {
		b += v.style[style]
	}
	return b
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
