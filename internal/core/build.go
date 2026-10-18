package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/cezomagoanlace29861-jpg/vp/internal/node"
)

// Set is a group of nodes laid out for the cores. Nodes Xray must run are
// served by an Xray sidecar on local SOCKS ports and appear in sing-box as
// plain SOCKS outbounds, so sing-box always fronts every node.
type Set struct {
	Nodes     []*node.Node
	Tags      []string // sing-box tag per node
	Outbounds []any    // sing-box outbounds, node order; configs must keep them first
	Endpoints []any    // sing-box endpoints, node order; configs must keep them first
	outIdx    []int    // node index per entry in Outbounds
	epIdx     []int    // node index per entry in Endpoints
	xrayIdx   []int    // node index per Xray node
	Xray      map[string]any
	XrayProbe string
}

// BuildOptions tune generated outbounds.
type BuildOptions struct {
	Fragment bool
}

// BuildSet lays nodes out for sing-box and, when needed, Xray.
func BuildSet(nodes []*node.Node, o BuildOptions) (*Set, error) {
	s := &Set{Nodes: nodes, Tags: make([]string, len(nodes))}
	var xrayNodes []int
	for i, n := range nodes {
		tag := "n" + strconv.Itoa(i)
		s.Tags[i] = tag
		if n.Engine() == node.EngineXray {
			xrayNodes = append(xrayNodes, i)
			continue
		}
		obj, ep := n.SingBox(tag, node.SingBoxOptions{Fragment: o.Fragment})
		if ep {
			s.Endpoints = append(s.Endpoints, obj)
			s.epIdx = append(s.epIdx, i)
		} else {
			s.Outbounds = append(s.Outbounds, obj)
			s.outIdx = append(s.outIdx, i)
		}
	}
	if len(xrayNodes) == 0 {
		return s, nil
	}
	ports, err := FreePorts(len(xrayNodes))
	if err != nil {
		return nil, err
	}
	var inbounds, outbounds, rules []any
	for k, i := range xrayNodes {
		n := nodes[i]
		xtag := "x" + strconv.Itoa(i)
		inTag := "in" + strconv.Itoa(i)
		inbounds = append(inbounds, map[string]any{
			"tag": inTag, "listen": "127.0.0.1", "port": ports[k], "protocol": "socks",
			"settings": map[string]any{"udp": true, "auth": "noauth"},
		})
		for _, ob := range n.Xray(xtag) {
			outbounds = append(outbounds, ob)
		}
		rules = append(rules, map[string]any{"inboundTag": []string{inTag}, "outboundTag": xtag})
		s.Outbounds = append(s.Outbounds, map[string]any{
			"type": "socks", "tag": s.Tags[i], "server": "127.0.0.1", "server_port": ports[k], "version": "5",
		})
		s.outIdx = append(s.outIdx, i)
		s.xrayIdx = append(s.xrayIdx, i)
	}
	outbounds = append(outbounds, map[string]any{"tag": "block", "protocol": "blackhole"})
	s.Xray = map[string]any{
		"log":       map[string]any{"loglevel": "warning"},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"routing":   map[string]any{"domainStrategy": "AsIs", "rules": rules},
	}
	s.XrayProbe = "127.0.0.1:" + strconv.Itoa(ports[0])
	return s, nil
}

// Running is a started set of cores.
type Running struct {
	Set      *Set
	SingBox  *Process
	Xray     *Process
	Rejected []*node.Node
}

// Stop stops both cores.
func (r *Running) Stop() {
	if r == nil {
		return
	}
	r.SingBox.Stop()
	if r.Xray != nil {
		r.Xray.Stop()
	}
}

// Alive reports whether every core is still running.
func (r *Running) Alive() bool {
	if r == nil || r.SingBox == nil || r.SingBox.Exited() {
		return false
	}
	return r.Xray == nil || !r.Xray.Exited()
}

// Runtime starts sets on the installed cores.
type Runtime struct {
	Bin    Binaries
	Dir    string // where configs are written
	OnLine func(core, line string)
}

var (
	sbIndexRe   = regexp.MustCompile(`(outbounds?|endpoints?)\[(\d+)\]`)
	xrayIndexRe = regexp.MustCompile(`outbound config with tag x(\d+)`)
)

// ConfigFunc builds a full sing-box config around a set. It must keep the
// set's Outbounds and Endpoints at the front of their arrays so startup
// errors can be traced back to nodes.
type ConfigFunc func(s *Set) map[string]any

// Run starts the cores for nodes. Nodes a core rejects are dropped and the
// rest are started again, so one broken server never blocks the others.
func (r *Runtime) Run(ctx context.Context, name string, nodes []*node.Node, o BuildOptions, cfg ConfigFunc, probe string) (*Running, error) {
	var rejected []*node.Node
	nodes = append([]*node.Node(nil), nodes...)
	for attempt := 0; attempt < 200; attempt++ {
		if len(nodes) == 0 {
			return nil, fmt.Errorf("no usable servers")
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		set, err := BuildSet(nodes, o)
		if err != nil {
			return nil, err
		}
		var xr *Process
		if set.Xray != nil {
			if r.Bin.Xray == "" {
				// Without Xray those nodes cannot run at all.
				nodes, rejected = dropIdx(nodes, set.xrayIdx, rejected)
				continue
			}
			xr, err = r.start(ctx, "xray", name, set.Xray, set.XrayProbe, func(path string) []string {
				return []string{"run", "-c", path}
			})
			if err != nil {
				if ee, ok := IsExit(err); ok {
					if m := xrayIndexRe.FindStringSubmatch(ee.Output); m != nil {
						i, _ := strconv.Atoi(m[1])
						nodes, rejected = dropIdx(nodes, []int{i}, rejected)
					} else {
						nodes, rejected = dropIdx(nodes, set.xrayIdx, rejected)
					}
					continue
				}
				return nil, err
			}
		}
		sb, err := r.start(ctx, "sing-box", name, cfg(set), probe, func(path string) []string {
			return []string{"run", "-c", path, "--disable-color"}
		})
		if err != nil {
			if xr != nil {
				xr.Stop()
			}
			if ee, ok := IsExit(err); ok {
				if m := sbIndexRe.FindStringSubmatch(ee.Output); m != nil {
					k, _ := strconv.Atoi(m[2])
					var idx []int
					if m[1][0] == 'o' {
						idx = set.outIdx
					} else {
						idx = set.epIdx
					}
					if k < len(idx) {
						nodes, rejected = dropIdx(nodes, []int{idx[k]}, rejected)
						continue
					}
				}
			}
			return nil, err
		}
		return &Running{Set: set, SingBox: sb, Xray: xr, Rejected: rejected}, nil
	}
	return nil, fmt.Errorf("too many rejected servers")
}

func dropIdx(nodes []*node.Node, idx []int, rejected []*node.Node) ([]*node.Node, []*node.Node) {
	drop := map[int]bool{}
	for _, i := range idx {
		drop[i] = true
	}
	var keep []*node.Node
	for i, n := range nodes {
		if drop[i] {
			rejected = append(rejected, n)
		} else {
			keep = append(keep, n)
		}
	}
	return keep, rejected
}

func (r *Runtime) start(ctx context.Context, coreName, name string, cfg map[string]any, probe string, args func(string) []string) (*Process, error) {
	b, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(r.Dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(r.Dir, name+"-"+coreName+".json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return nil, err
	}
	bin := r.Bin.SingBox
	if coreName == "xray" {
		bin = r.Bin.Xray
	}
	var onLine func(string)
	if r.OnLine != nil {
		onLine = func(l string) { r.OnLine(coreName, l) }
	}
	var env []string
	if coreName == "xray" {
		env = []string{"XRAY_LOCATION_ASSET=" + filepath.Dir(bin)}
	}
	p, err := Start(coreName, bin, args(path), env, onLine)
	if err != nil {
		return nil, err
	}
	if err := p.WaitListening(ctx, probe, 15*time.Second); err != nil {
		p.Stop()
		return nil, err
	}
	return p, nil
}
