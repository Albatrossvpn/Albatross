package node

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestRealData parses a large dump of real share links and has the real
// cores validate every generated outbound. It is opt-in:
//
//	SKYROAD_LINKS=links.txt SING_BOX=/path/sing-box XRAY=/path/xray go test -run RealData ./internal/node
func TestRealData(t *testing.T) {
	path := os.Getenv("SKYROAD_LINKS")
	if path == "" {
		t.Skip("SKYROAD_LINKS not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	links := ExtractLinks(string(raw))
	errs := map[string]int{}
	examples := map[string][]string{}
	engines := map[Engine]int{}
	seen := map[string]bool{}
	var sb, xr []*Node
	for _, l := range links {
		n, err := ParseLink(l)
		if err != nil {
			msg := err.Error()
			if i := strings.IndexAny(msg, "\"0123456789"); i > 0 {
				msg = msg[:i]
			}
			errs[msg]++
			if len(examples[msg]) < 3 {
				examples[msg] = append(examples[msg], truncate(l, 200))
			}
			continue
		}
		if seen[n.Key()] {
			continue
		}
		seen[n.Key()] = true
		e := n.Engine()
		engines[e]++
		switch e {
		case EngineSingBox:
			sb = append(sb, n)
		case EngineXray:
			xr = append(xr, n)
		}
	}
	t.Logf("links=%d unique=%d engines=%v", len(links), len(seen), engines)
	var keys []string
	for k := range errs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return errs[keys[i]] > errs[keys[j]] })
	for _, k := range keys {
		t.Logf("parse error %6d  %s", errs[k], k)
		for _, e := range examples[k] {
			t.Logf("        %s", e)
		}
	}
	if bin := os.Getenv("SING_BOX"); bin != "" {
		bad := checkSingBox(t, bin, sb)
		t.Logf("sing-box rejected %d of %d", len(bad), len(sb))
		for i, b := range bad {
			if i >= 25 {
				break
			}
			t.Logf("  %s\n     %s", b.err, truncate(b.n.Source, 220))
		}
	}
	if bin := os.Getenv("XRAY"); bin != "" {
		bad := checkXray(t, bin, xr)
		t.Logf("xray rejected %d of %d", len(bad), len(xr))
		for i, b := range bad {
			if i >= 25 {
				break
			}
			t.Logf("  %s\n     %s", b.err, truncate(b.n.Source, 220))
		}
	}
}

func TestVPNGateRealData(t *testing.T) {
	path, bin := os.Getenv("VPNGATE_CSV"), os.Getenv("SING_BOX")
	if path == "" || bin == "" {
		t.Skip("VPNGATE_CSV or SING_BOX not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	servers := ParseVPNGate(string(raw))
	if len(servers) == 0 {
		t.Fatal("no servers parsed")
	}
	var nodes []*Node
	for _, s := range servers {
		back, err := ParseLink(s.Node.Link())
		if err != nil || back.Key() != s.Node.Key() {
			t.Fatalf("round trip failed: %v", err)
		}
		nodes = append(nodes, back)
	}
	bad := checkSingBox(t, bin, nodes)
	t.Logf("vpngate: %d servers, sing-box rejected %d", len(nodes), len(bad))
	for _, b := range bad {
		t.Logf("  %s", b.err)
	}
	if len(bad) > 0 {
		t.Fail()
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

type rejected struct {
	n   *Node
	err string
}

var idxRe = regexp.MustCompile(`(outbounds?|endpoints?)\[(\d+)\]`)

func checkSingBox(t *testing.T, bin string, nodes []*Node) []rejected {
	dir := t.TempDir()
	var bad []rejected
	for len(nodes) > 0 {
		var outs, eps []any
		var outIdx, epIdx []int
		for i, n := range nodes {
			obj, ep := n.SingBox("n"+strconv.Itoa(i), SingBoxOptions{})
			if ep {
				eps = append(eps, obj)
				epIdx = append(epIdx, i)
			} else {
				outs = append(outs, obj)
				outIdx = append(outIdx, i)
			}
		}
		cfg := map[string]any{
			"log":       map[string]any{"level": "error"},
			"dns":       map[string]any{"servers": []any{map[string]any{"type": "local", "tag": "local"}}},
			"outbounds": outs,
			"endpoints": eps,
			"route":     map[string]any{"default_domain_resolver": "local"},
		}
		b, _ := json.Marshal(cfg)
		file := filepath.Join(dir, "c.json")
		os.WriteFile(file, b, 0o600)
		out, err := exec.Command(bin, "check", "-c", file).CombinedOutput()
		if err == nil {
			return bad
		}
		m := idxRe.FindStringSubmatch(string(out))
		if m == nil {
			t.Fatalf("sing-box check failed without index: %s", out)
		}
		k, _ := strconv.Atoi(m[2])
		var i int
		if strings.HasPrefix(m[1], "outbound") {
			i = outIdx[k]
		} else {
			i = epIdx[k]
		}
		bad = append(bad, rejected{nodes[i], strings.TrimSpace(lastLine(string(out)))})
		nodes = append(nodes[:i:i], nodes[i+1:]...)
	}
	return bad
}

var xrayIdxRe = regexp.MustCompile(`failed to build outbound config with tag (x\d+)(-dial)?`)

func checkXray(t *testing.T, bin string, nodes []*Node) []rejected {
	dir := t.TempDir()
	var bad []rejected
	for len(nodes) > 0 {
		var outs []any
		for i, n := range nodes {
			for _, o := range n.Xray("x" + strconv.Itoa(i)) {
				outs = append(outs, o)
			}
		}
		b, _ := json.Marshal(map[string]any{"log": map[string]any{"loglevel": "error"}, "outbounds": outs})
		file := filepath.Join(dir, "x.json")
		os.WriteFile(file, b, 0o600)
		out, err := exec.Command(bin, "run", "-test", "-c", file).CombinedOutput()
		if err == nil {
			return bad
		}
		m := xrayIdxRe.FindStringSubmatch(string(out))
		if m == nil {
			// Xray does not always name the tag; bisect instead.
			if len(nodes) == 1 {
				bad = append(bad, rejected{nodes[0], strings.TrimSpace(lastLine(string(out)))})
				return bad
			}
			half := len(nodes) / 2
			bad = append(bad, checkXray(t, bin, nodes[:half])...)
			bad = append(bad, checkXray(t, bin, nodes[half:])...)
			return bad
		}
		i, _ := strconv.Atoi(strings.TrimPrefix(m[1], "x"))
		bad = append(bad, rejected{nodes[i], fmt.Sprint(strings.TrimSpace(lastLine(string(out))))})
		nodes = append(nodes[:i:i], nodes[i+1:]...)
	}
	return bad
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
