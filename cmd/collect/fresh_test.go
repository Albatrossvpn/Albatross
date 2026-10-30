package main

import "testing"

func protoEntries(proto string, n int, fresh bool) []*entry {
	var out []*entry
	for i := 0; i < n; i++ {
		out = append(out, &entry{proto: proto, fresh: fresh, pop: 1})
	}
	return out
}

// What the internet holds of a rare protocol reaches fresh.txt whichever
// source carried it; the common protocols still need a priority source.
func TestSplitFreshKeepsRareProtocolsInTheListEveryAppLoads(t *testing.T) {
	var list []*entry
	list = append(list, protoEntries("juicity", 13, false)...)
	list = append(list, protoEntries("snell", 3, false)...)
	list = append(list, protoEntries("vless", 50, false)...)
	list = append(list, protoEntries("vless", 5, true)...)
	fresh, rest := splitFresh(list, nil, map[*entry]bool{}, 60000)
	count := func(es []*entry, proto string) int {
		n := 0
		for _, e := range es {
			if e.proto == proto {
				n++
			}
		}
		return n
	}
	if count(fresh, "juicity") != 13 || count(fresh, "snell") != 3 {
		t.Errorf("rare servers in fresh: juicity %d, snell %d", count(fresh, "juicity"), count(fresh, "snell"))
	}
	if count(fresh, "vless") != 5 || count(rest, "vless") != 50 {
		t.Errorf("vless: %d fresh, %d rest; only a priority source's go to fresh", count(fresh, "vless"), count(rest, "vless"))
	}
	if len(fresh)+len(rest) != len(list) {
		t.Errorf("every server goes to exactly one list: %d + %d != %d", len(fresh), len(rest), len(list))
	}
}

// A rare protocol is capped per protocol, a server only dud sources carry
// stays out, the overall cap holds, and the lane picks are not doubled.
func TestSplitFreshLimits(t *testing.T) {
	list := protoEntries("wireguard", rareFresh+40, false)
	fresh, rest := splitFresh(list, nil, map[*entry]bool{}, 60000)
	if len(fresh) != rareFresh || len(rest) != 40 {
		t.Errorf("a rare protocol is capped at %d: %d fresh, %d rest", rareFresh, len(fresh), len(rest))
	}

	dud := &entry{proto: "naive", pop: 1, dud: 1}
	ok := &entry{proto: "naive", pop: 2, dud: 1}
	fresh, rest = splitFresh([]*entry{dud, ok}, nil, map[*entry]bool{}, 60000)
	if len(fresh) != 1 || fresh[0] != ok || len(rest) != 1 || rest[0] != dud {
		t.Errorf("only dud sources carry the first: fresh %v, rest %v", fresh, rest)
	}

	picked := &entry{proto: "juicity", pop: 1, lane: true}
	other := protoEntries("juicity", 4, false)
	fresh, rest = splitFresh(append([]*entry{picked}, other...), []*entry{picked}, map[*entry]bool{picked: true}, 3)
	if len(fresh) != 4 || fresh[0] != picked || len(rest) != 1 {
		t.Errorf("the lane pick comes first and is not doubled, three more fit under the cap of 3: %d fresh, %d rest", len(fresh), len(rest))
	}
}
