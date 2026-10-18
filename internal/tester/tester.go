// Package tester measures which servers actually work: a cheap TCP probe
// first, then a real HTTPS request through each server that also reveals
// its exit IP and country.
package tester

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cezomagoanlace29861-jpg/vp/internal/core"
	"github.com/cezomagoanlace29861-jpg/vp/internal/node"
)

// Result of a real test.
type Result struct {
	Node     *node.Node
	OK       bool
	Delay    time.Duration
	Country  string
	ExitIP   string
	Err      string
	Rejected bool // the core refused the config
}

// Tester runs probes and real tests.
type Tester struct {
	Runtime        *core.Runtime
	Concurrency    int
	Timeout        time.Duration
	TCPConcurrency int
	TCPTimeout     time.Duration
	Fragment       bool
	FragmentFor    func(*node.Node) bool
	Spoof          string
	// LocalDNS resolves server names where the core cannot ask the system
	// (Android); empty means the system's resolver.
	LocalDNS string
}

// TCPProbe dials every TCP-based node and returns the connect time, or -1
// when the server could not be reached. UDP-based nodes are reported as 0
// because a TCP probe says nothing about them.
func (t *Tester) TCPProbe(ctx context.Context, nodes []*node.Node) []time.Duration {
	out := make([]time.Duration, len(nodes))
	conc := t.TCPConcurrency
	if conc <= 0 {
		conc = 128
	}
	timeout := t.TCPTimeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	// Many nodes share an endpoint; probe each endpoint once.
	byEndpoint := map[string][]int{}
	var order []string
	for i, n := range nodes {
		if n.UDPBased() {
			continue
		}
		ep := n.Endpoint()
		if _, ok := byEndpoint[ep]; !ok {
			order = append(order, ep)
		}
		byEndpoint[ep] = append(byEndpoint[ep], i)
	}
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, ep := range order {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(ep string) {
			defer func() { <-sem; wg.Done() }()
			d := probe(ctx, ep, timeout)
			mu.Lock()
			for _, i := range byEndpoint[ep] {
				out[i] = d
			}
			mu.Unlock()
		}(ep)
	}
	wg.Wait()
	return out
}

func probe(ctx context.Context, addr string, timeout time.Duration) time.Duration {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return -1
	}
	c.Close()
	el := time.Since(start)
	if el <= 0 {
		el = time.Millisecond
	}
	return el
}

// TestEndpoints answer with the caller's IP and country. Cloudflare's trace
// is fast and unlimited; ipinfo is the fallback for servers that cannot
// reach Cloudflare (some Cloudflare Workers based configs).
var TestEndpoints = []string{
	"https://www.cloudflare.com/cdn-cgi/trace",
	"https://ipinfo.io/json",
}

// Batch starts a temporary core holding every node, then tests all of them
// in parallel. Each finished test is reported through onResult.
func (t *Tester) Batch(ctx context.Context, name string, nodes []*node.Node, onResult func(Result)) error {
	if len(nodes) == 0 {
		return nil
	}
	ports, err := core.FreePorts(1)
	if err != nil {
		return err
	}
	port := ports[0]
	pass := randomHex(8)
	opts := core.BuildOptions{Fragment: t.Fragment, FragmentFor: t.FragmentFor, Spoof: t.Spoof}
	run, err := t.Runtime.Run(ctx, name, nodes, opts, func(s *core.Set) map[string]any {
		return testConfig(s, port, pass, t.LocalDNS)
	}, "127.0.0.1:"+strconv.Itoa(port))
	if run != nil {
		for _, n := range run.Rejected {
			onResult(Result{Node: n, Rejected: true, Err: "rejected by core"})
		}
		for _, n := range run.Failed {
			onResult(Result{Node: n, Err: "could not connect"})
		}
	}
	if err != nil {
		return err
	}
	defer run.Stop()
	conc := t.Concurrency
	if conc <= 0 {
		conc = 32
	}
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	for i, n := range run.Set.Nodes {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(n *node.Node, tag string) {
			defer func() { <-sem; wg.Done() }()
			if !run.Alive() {
				return
			}
			proxy := &url.URL{Scheme: "http", User: url.UserPassword(tag, pass), Host: "127.0.0.1:" + strconv.Itoa(port)}
			limit := timeout
			if n.Engine() == node.EngineWarpPlus {
				limit = 3 * timeout
			}
			r := Probe(ctx, proxy, limit)
			if !run.Alive() {
				// The core crashed; the result says nothing about the node.
				return
			}
			r.Node = n
			onResult(r)
		}(n, run.Set.Tags[i])
	}
	wg.Wait()
	return nil
}

// Probe makes one real request through a proxy and returns delay, exit IP
// and country.
func Probe(ctx context.Context, proxy *url.URL, timeout time.Duration) Result {
	var lastErr error
	for _, endpoint := range TestEndpoints {
		start := time.Now()
		ip, country, err := fetchTrace(ctx, proxy, endpoint, timeout)
		if err == nil {
			return Result{OK: true, Delay: time.Since(start), Country: country, ExitIP: ip}
		}
		lastErr = err
		if ctx.Err() != nil || isTimeout(err) {
			// A server that times out once will not answer the fallback.
			break
		}
	}
	return Result{Err: shortErr(lastErr)}
}

func fetchTrace(ctx context.Context, proxy *url.URL, endpoint string, timeout time.Duration) (ip, country string, err error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tr := &http.Transport{
		Proxy:                 http.ProxyURL(proxy),
		DisableKeepAlives:     true,
		ForceAttemptHTTP2:     false,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		DialContext:           (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
	}
	defer tr.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if strings.Contains(endpoint, "cdn-cgi/trace") {
		return parseTrace(string(body))
	}
	var j struct {
		IP      string `json:"ip"`
		Country string `json:"country"`
	}
	if err := json.Unmarshal(body, &j); err != nil || j.IP == "" {
		return "", "", errors.New("bad ip response")
	}
	return j.IP, strings.ToUpper(j.Country), nil
}

func parseTrace(body string) (ip, country string, err error) {
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch k {
		case "ip":
			ip = v
		case "loc":
			country = strings.ToUpper(v)
		}
	}
	if ip == "" {
		return "", "", errors.New("bad trace response")
	}
	if country == "XX" || country == "T1" {
		country = ""
	}
	return ip, country, nil
}

func isTimeout(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}

func shortErr(err error) string {
	if err == nil {
		return "failed"
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "deadline exceeded"), strings.Contains(s, "timeout"):
		return "timeout"
	case strings.Contains(s, "EOF"):
		return "connection closed"
	case strings.Contains(s, "refused"):
		return "refused"
	case strings.Contains(s, "reset"):
		return "reset"
	}
	if len(s) > 80 {
		s = s[len(s)-80:]
	}
	return s
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// DNSServer is the sing-box resolver for direct lookups: the system's, or
// a plain DNS server at addr ("1.2.3.4" or "1.2.3.4:53") where the core
// cannot ask the system.
func DNSServer(addr string) map[string]any {
	if addr == "" {
		return map[string]any{"type": "local", "tag": "local"}
	}
	s := map[string]any{"type": "udp", "tag": "local", "server": addr}
	if host, port, err := net.SplitHostPort(addr); err == nil {
		p, _ := strconv.Atoi(port)
		s["server"], s["server_port"] = host, p
	}
	return s
}

func testConfig(s *core.Set, port int, pass, localDNS string) map[string]any {
	users := make([]any, 0, len(s.Tags))
	rules := make([]any, 0, len(s.Tags)+1)
	for _, tag := range s.Tags {
		users = append(users, map[string]any{"username": tag, "password": pass})
		rules = append(rules, map[string]any{"auth_user": []string{tag}, "outbound": tag})
	}
	rules = append(rules, map[string]any{"action": "reject"})
	cfg := map[string]any{
		"log": map[string]any{"level": "warn", "timestamp": false},
		"dns": map[string]any{
			"servers": []any{DNSServer(localDNS)},
		},
		"inbounds": []any{map[string]any{
			"type": "mixed", "tag": "test-in", "listen": "127.0.0.1", "listen_port": port, "users": users,
		}},
		"outbounds": s.Outbounds,
		"route": map[string]any{
			"rules":                   rules,
			"default_domain_resolver": "local",
			// Bind to the physical interface so tests bypass our own TUN.
			"auto_detect_interface": true,
		},
	}
	if len(s.Endpoints) > 0 {
		cfg["endpoints"] = s.Endpoints
	}
	return cfg
}
