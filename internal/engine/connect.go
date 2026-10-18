package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cezomagoanlace29861-jpg/vp/internal/core"
	"github.com/cezomagoanlace29861-jpg/vp/internal/geo"
	"github.com/cezomagoanlace29861-jpg/vp/internal/node"
	"github.com/cezomagoanlace29861-jpg/vp/internal/tester"
)

// groupSize is how many servers one connection keeps ready. sing-box's
// urltest keeps using the lowest-delay one and fails over instantly.
const groupSize = 12

type connection struct {
	country     string
	mode        string
	proxyAddr   string
	running     *core.Running
	servers     []*Server
	tags        map[string]string // sing-box tag -> server key
	clashAddr   string
	clashSecret string
	started     time.Time
	sysProxy    bool
	cancel      context.CancelFunc

	mu      sync.Mutex
	active  string // key of the server urltest picked
	delay   int
	exitIP  string
	exitCC  string
	up      int64
	down    int64
	upRate  int64
	dnRate  int64
	healthy bool
}

func (c *connection) ready() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.healthy
}

func (c *connection) activeKey() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active
}

func (c *connection) uses(key string) bool {
	for _, s := range c.servers {
		if s.Key == key {
			return true
		}
	}
	return false
}

// Connect connects to the fastest servers in country ("" = anywhere). If
// none are known yet it searches until it finds some.
func (e *Engine) Connect(country string) error {
	country = strings.ToUpper(strings.TrimSpace(country))
	e.mu.Lock()
	e.target = country
	e.settings.Country = country
	e.wantConnect = true
	e.connGen++
	gen := e.connGen
	mode := e.settings.Mode
	e.mu.Unlock()
	saveJSON(filepath.Join(e.dir, "settings.json"), e.Settings())
	if mode == "tun" && !e.opt.IsAdmin() {
		e.setStatus(StatusIdle, "")
		return ErrNeedAdmin
	}
	e.poke()
	return e.connectLoop(gen, country, nil)
}

// ConnectServer connects to one specific working server.
func (e *Engine) ConnectServer(key string) error {
	e.mu.Lock()
	var s *Server
	for _, g := range e.good {
		if g.Key == key {
			s = g
		}
	}
	if s == nil {
		e.mu.Unlock()
		return fmt.Errorf("server not found")
	}
	e.target = s.Country
	e.wantConnect = true
	e.connGen++
	gen := e.connGen
	e.mu.Unlock()
	return e.connectLoop(gen, s.Country, []*Server{s})
}

func countryName(code string) string {
	if code == "" {
		return "سریع‌ترین کشور"
	}
	c, _ := geo.Lookup(code)
	return c.FA
}

func (e *Engine) connectLoop(gen int, country string, pinned []*Server) error {
	name := countryName(country)
	deadline := time.Now().Add(3 * time.Minute)
	var exclude = map[string]bool{}
	for attempt := 0; ; attempt++ {
		e.mu.Lock()
		want := e.wantConnect && e.target == country && e.connGen == gen
		e.mu.Unlock()
		if !want || e.ctx.Err() != nil {
			return errors.New("cancelled")
		}
		picks := pinned
		if picks == nil {
			picks = e.bestServers(country, groupSize, exclude)
		}
		if len(picks) == 0 {
			e.setStatus(StatusSearching, fmt.Sprintf("در حال جستجوی سرور سالم برای %s…", name))
			if time.Now().After(deadline) {
				e.setStatus(StatusSearching, fmt.Sprintf("هنوز سروری برای %s پیدا نشده؛ جستجو ادامه دارد و به محض پیدا شدن وصل می‌شوید.", name))
			}
			select {
			case <-e.ctx.Done():
				return e.ctx.Err()
			case <-time.After(2 * time.Second):
			}
			continue
		}
		e.setStatus(StatusConnecting, fmt.Sprintf("در حال اتصال به %s…", name))
		err := e.start(gen, country, picks)
		if err == nil {
			return nil
		}
		if errors.Is(err, errStale) {
			return err
		}
		e.logf("warn", "اتصال ناموفق: %v", err)
		for _, s := range picks {
			exclude[s.Key] = true
			if errors.Is(err, errServers) {
				e.mu.Lock()
				s.Fails++
				e.mu.Unlock()
			}
		}
		if pinned != nil {
			e.setStatus(StatusIdle, "این سرور الان کار نمی‌کند.")
			e.mu.Lock()
			e.wantConnect = false
			e.mu.Unlock()
			return err
		}
		if attempt > 6 {
			exclude = map[string]bool{}
		}
	}
}

// bestServers picks the lowest-delay usable servers in a country.
func (e *Engine) bestServers(country string, n int, exclude map[string]bool) []*Server {
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	var list []*Server
	for _, s := range e.good {
		if exclude[s.Key] || !s.usable(now) || s.Country == "IR" {
			continue
		}
		if country != "" && s.Country != country {
			continue
		}
		if s.Plain && !e.settings.AllowPlain {
			continue
		}
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].rank(now) < list[j].rank(now) })
	// WireGuard peers sharing a private key fight over one session, and each
	// warp-plus method is a whole process; keep the group light.
	var out []*Server
	keys := map[string]bool{}
	warpPlus := 0
	for _, s := range list {
		if len(out) >= n {
			break
		}
		nd := s.Node()
		if nd == nil {
			continue
		}
		if nd.Proto == "wireguard" {
			if keys[nd.PrivateKey] {
				continue
			}
			keys[nd.PrivateKey] = true
		}
		if nd.Proto == "warpplus" {
			if warpPlus >= 2 {
				continue
			}
			warpPlus++
		}
		out = append(out, s)
	}
	return out
}

var (
	// errStale means the user asked for something else meanwhile.
	errStale = errors.New("superseded")
	// errServers means the servers, not the local setup, failed.
	errServers = errors.New("servers failed")
)

// portFree reports whether a local TCP port can be bound.
func portFree(listen string, port int) bool {
	l, err := net.Listen("tcp", net.JoinHostPort(listen, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

// start runs the cores for a group of servers and switches over to them.
func (e *Engine) start(gen int, country string, servers []*Server) error {
	e.mu.Lock()
	set := e.settings
	old := e.conn
	e.mu.Unlock()

	var nodes []*node.Node
	fragFor := map[*node.Node]bool{}
	byNode := map[*node.Node]*Server{}
	for _, s := range servers {
		n := s.runNode()
		if n == nil {
			continue
		}
		nodes = append(nodes, n)
		fragFor[n] = s.Fragment
		byNode[n] = s
	}
	if len(nodes) == 0 {
		return errors.New("no valid servers")
	}
	// The old connection must release the proxy port first.
	if old != nil {
		e.stopConn(old, false)
	}
	listen := "127.0.0.1"
	if set.AllowLAN {
		listen = "0.0.0.0"
	}
	proxyPort := set.ProxyPort
	if !portFree(listen, proxyPort) {
		// Another program (often another VPN client) holds the port.
		if p, err := core.FreePorts(1); err == nil {
			e.logf("warn", "پورت %d در اختیار برنامه دیگری است؛ از پورت %d استفاده شد.", proxyPort, p[0])
			proxyPort = p[0]
		}
	}
	ports, err := core.FreePorts(1)
	if err != nil {
		return err
	}
	c := &connection{
		country:     country,
		mode:        set.Mode,
		proxyAddr:   "127.0.0.1:" + strconv.Itoa(proxyPort),
		clashAddr:   "127.0.0.1:" + strconv.Itoa(ports[0]),
		clashSecret: randomToken(),
		started:     time.Now(),
		tags:        map[string]string{},
	}
	opts := connOptions{
		listen: listen, port: proxyPort, tun: set.Mode == "tun", bypassIran: set.BypassIran,
		blockAds: set.BlockAds, rulesDir: filepath.Join(e.dir, "rules"), clashAddr: c.clashAddr,
		clashSecret: c.clashSecret, exe: e.opt.ExeName,
	}
	build := core.BuildOptions{Fragment: set.Fragment == "on", FragmentFor: func(n *node.Node) bool { return fragFor[n] }}
	if set.SpoofSNI != "" && e.opt.IsAdmin() {
		build.Spoof = set.SpoofSNI
	}
	ctx, cancel := context.WithTimeout(e.ctx, 30*time.Second)
	defer cancel()
	run, err := e.rt.Run(ctx, "connect", nodes, build, func(s *core.Set) map[string]any {
		return connectConfig(s, opts)
	}, c.proxyAddr)
	if err != nil {
		return err
	}
	for _, n := range run.Rejected {
		if s := byNode[n]; s != nil {
			e.mu.Lock()
			s.Fails = 3
			e.mu.Unlock()
		}
	}
	c.running = run
	for i, n := range run.Set.Nodes {
		if s := byNode[n]; s != nil {
			c.servers = append(c.servers, s)
			c.tags[run.Set.Tags[i]] = s.Key
		}
	}
	// Prove the connection works before telling anyone it does.
	r := tester.Probe(e.ctx, &url.URL{Scheme: "http", Host: c.proxyAddr}, 12*time.Second)
	if !r.OK {
		run.Stop()
		return fmt.Errorf("%w: test through the new connection failed: %s", errServers, r.Err)
	}
	e.mu.Lock()
	stale := e.connGen != gen
	e.mu.Unlock()
	if stale {
		run.Stop()
		return errStale
	}
	c.exitIP, c.exitCC, c.delay, c.healthy = r.ExitIP, r.Country, int(r.Delay/time.Millisecond), true
	if set.Mode == "proxy" {
		bypass := []string{"localhost", "127.*", "10.*", "172.16.*", "172.17.*", "172.18.*", "172.19.*", "172.2*", "172.30.*", "172.31.*", "192.168.*", "<local>"}
		if set.BypassIran {
			bypass = append(bypass, "*.ir")
		}
		if err := e.opt.SysProxy.Set(c.proxyAddr, bypass); err != nil {
			e.logf("warn", "تنظیم پروکسی سیستم ناموفق بود: %v", err)
		} else {
			c.sysProxy = true
		}
	}
	mctx, mcancel := context.WithCancel(e.ctx)
	c.cancel = mcancel
	e.mu.Lock()
	e.conn = c
	e.mu.Unlock()
	e.updateActive(c)
	where := countryName(r.Country)
	if r.Country == "" {
		where = countryName(country)
	}
	e.setStatus(StatusConnected, fmt.Sprintf("وصل هستید — %s", where))
	e.logf("info", "وصل شد به %s با %d سرور آماده (تأخیر %d میلی‌ثانیه).", where, len(c.servers), c.delay)
	go e.monitor(mctx, c)
	// Sources blocked on the direct path (Telegram, GitHub) can now be
	// read through the VPN.
	e.triggerRefill()
	return nil
}

// Disconnect stops the VPN and restores system settings.
func (e *Engine) Disconnect() {
	e.mu.Lock()
	e.wantConnect = false
	e.connGen++
	c := e.conn
	e.mu.Unlock()
	if c != nil {
		e.stopConn(c, true)
	}
	e.setStatus(StatusIdle, "")
}

func (e *Engine) stopConn(c *connection, clearProxy bool) {
	if c.cancel != nil {
		c.cancel()
	}
	if c.sysProxy && clearProxy {
		e.opt.SysProxy.Clear()
	}
	c.running.Stop()
	e.mu.Lock()
	if e.conn == c {
		e.conn = nil
	}
	e.mu.Unlock()
}

// monitor watches a connection: traffic, which server is in use, health,
// and whether faster servers have appeared.
func (e *Engine) monitor(ctx context.Context, c *connection) {
	traffic := time.NewTicker(time.Second)
	defer traffic.Stop()
	health := time.NewTicker(20 * time.Second)
	defer health.Stop()
	upgrade := time.NewTicker(4 * time.Minute)
	defer upgrade.Stop()
	failures, ticks := 0, 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-traffic.C:
			e.updateTraffic(c)
			ticks++
			if ticks%5 == 0 {
				e.updateActive(c)
			}
		case <-health.C:
			if !c.running.Alive() {
				e.logf("warn", "هسته متوقف شد؛ اتصال دوباره…")
				e.reconnect(c, "")
				return
			}
			e.updateActive(c)
			r := tester.Probe(ctx, &url.URL{Scheme: "http", Host: c.proxyAddr}, 12*time.Second)
			if ctx.Err() != nil {
				return
			}
			if r.OK {
				failures = 0
				c.mu.Lock()
				c.healthy, c.exitIP, c.delay = true, r.ExitIP, int(r.Delay/time.Millisecond)
				if r.Country != "" {
					c.exitCC = r.Country
				}
				c.mu.Unlock()
				continue
			}
			failures++
			if failures >= 2 {
				e.logf("warn", "اتصال قطع شده؛ جابه‌جایی به سرورهای دیگر…")
				e.reconnect(c, c.activeKey())
				return
			}
		case <-upgrade.C:
			e.mu.Lock()
			auto := e.settings.AutoSwitch
			e.mu.Unlock()
			if !auto || len(c.servers) == 1 && c.country == "" {
				continue
			}
			if e.betterAvailable(c) {
				e.logf("info", "سرورهای سریع‌تری پیدا شد؛ جابه‌جایی…")
				e.reconnect(c, "")
				return
			}
		}
	}
}

// betterAvailable reports whether the best known servers beat the current
// group by a clear margin, or the group has mostly died.
func (e *Engine) betterAvailable(c *connection) bool {
	now := time.Now()
	best := e.bestServers(c.country, groupSize, nil)
	if len(best) == 0 {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	alive := 0
	for _, s := range c.servers {
		if s.usable(now) {
			alive++
		}
	}
	if alive < 2 && len(best) > alive {
		return true
	}
	inGroup := map[string]bool{}
	for _, s := range c.servers {
		inGroup[s.Key] = true
	}
	c.mu.Lock()
	cur := c.delay
	c.mu.Unlock()
	for _, s := range best[:min(3, len(best))] {
		if !inGroup[s.Key] && cur > 0 && float64(s.Delay) < float64(cur)*0.6 && cur-s.Delay > 80 {
			return true
		}
	}
	return false
}

func (e *Engine) reconnect(c *connection, failedKey string) {
	e.setStatus(StatusReconnecting, "در حال جابه‌جایی به سرور بهتر…")
	e.mu.Lock()
	if failedKey != "" {
		for _, s := range e.good {
			if s.Key == failedKey {
				s.Fails++
			}
		}
	}
	country := c.country
	gen := e.connGen
	e.mu.Unlock()
	e.stopConn(c, false)
	go func() {
		if err := e.connectLoop(gen, country, nil); err != nil {
			e.mu.Lock()
			still := e.conn == nil
			e.mu.Unlock()
			if still {
				e.opt.SysProxy.Clear()
			}
		}
	}()
}

func (e *Engine) clashGet(c *connection, path string, v any) error {
	req, _ := http.NewRequest(http.MethodGet, "http://"+c.clashAddr+path, nil)
	req.Header.Set("Authorization", "Bearer "+c.clashSecret)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func (e *Engine) updateActive(c *connection) {
	var p struct {
		Now     string `json:"now"`
		History []struct {
			Delay int `json:"delay"`
		} `json:"history"`
	}
	if e.clashGet(c, "/proxies/proxy", &p) != nil {
		return
	}
	c.mu.Lock()
	if key, ok := c.tags[p.Now]; ok {
		c.active = key
	}
	c.mu.Unlock()
}

func (e *Engine) updateTraffic(c *connection) {
	var conns struct {
		Up   int64 `json:"uploadTotal"`
		Down int64 `json:"downloadTotal"`
	}
	if e.clashGet(c, "/connections", &conns) != nil {
		return
	}
	c.mu.Lock()
	if c.up > 0 || c.down > 0 {
		c.upRate = max(conns.Up-c.up, 0)
		c.dnRate = max(conns.Down-c.down, 0)
	}
	c.up, c.down = conns.Up, conns.Down
	c.mu.Unlock()
}

type connOptions struct {
	listen      string
	port        int
	tun         bool
	bypassIran  bool
	blockAds    bool
	rulesDir    string
	clashAddr   string
	clashSecret string
	exe         string
}

// connectConfig builds the sing-box config for a live connection.
func connectConfig(s *core.Set, o connOptions) map[string]any {
	outbounds := append([]any{}, s.Outbounds...)
	outbounds = append(outbounds,
		map[string]any{
			"type": "urltest", "tag": "proxy", "outbounds": s.Tags,
			"url": "https://www.gstatic.com/generate_204", "interval": "1m", "tolerance": 50,
			"idle_timeout": "30m", "interrupt_exist_connections": false,
		},
		map[string]any{"type": "direct", "tag": "direct"},
	)
	inbounds := []any{map[string]any{"type": "mixed", "tag": "mixed-in", "listen": o.listen, "listen_port": o.port}}
	if o.tun {
		inbounds = append(inbounds, map[string]any{
			"type": "tun", "tag": "tun-in", "interface_name": "Azadrah",
			"address": []string{"172.19.0.1/30", "fdfe:dcba:9876::1/126"},
			"mtu":     9000, "auto_route": true, "strict_route": true, "stack": "mixed",
		})
	}
	var ruleSets []any
	rules := []any{
		map[string]any{"action": "sniff"},
		map[string]any{"protocol": "dns", "action": "hijack-dns"},
		map[string]any{"ip_is_private": true, "outbound": "direct"},
	}
	if o.tun {
		// Our own app and the cores' test instances must not loop
		// through the tunnel.
		procs := []string{"sing-box.exe", "xray.exe", "warp-plus.exe", "sing-box", "xray"}
		if o.exe != "" {
			procs = append(procs, o.exe)
		}
		rules = append(rules, map[string]any{"process_name": procs, "outbound": "direct"})
	}
	if o.blockAds {
		ruleSets = append(ruleSets, localRuleSet("geosite-ads", o.rulesDir))
		rules = append(rules, map[string]any{"rule_set": []string{"geosite-ads"}, "action": "reject"})
	}
	dnsRules := []any{}
	if o.bypassIran {
		ruleSets = append(ruleSets, localRuleSet("geosite-ir", o.rulesDir), localRuleSet("geoip-ir", o.rulesDir))
		rules = append(rules, map[string]any{
			"domain_suffix": []string{".ir", ".xn--mgba3a4f16a"},
			"rule_set":      []string{"geosite-ir", "geoip-ir"},
			"outbound":      "direct",
		})
		dnsRules = append(dnsRules, map[string]any{
			"domain_suffix": []string{".ir", ".xn--mgba3a4f16a"}, "rule_set": []string{"geosite-ir"},
			"action": "route", "server": "local",
		})
	}
	route := map[string]any{
		"rules":                   rules,
		"final":                   "proxy",
		"auto_detect_interface":   true,
		"default_domain_resolver": "local",
	}
	if len(ruleSets) > 0 {
		route["rule_set"] = ruleSets
	}
	dns := map[string]any{
		"servers": []any{
			map[string]any{"type": "local", "tag": "local"},
			map[string]any{"type": "https", "tag": "remote", "server": "1.1.1.1", "detour": "proxy"},
		},
		"rules":    dnsRules,
		"final":    "remote",
		"strategy": "ipv4_only",
	}
	cfg := map[string]any{
		"log":       map[string]any{"level": "warn", "timestamp": false},
		"dns":       dns,
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"route":     route,
		"experimental": map[string]any{
			"clash_api": map[string]any{"external_controller": o.clashAddr, "secret": o.clashSecret},
		},
	}
	if len(s.Endpoints) > 0 {
		cfg["endpoints"] = s.Endpoints
	}
	return cfg
}

func localRuleSet(tag, dir string) map[string]any {
	return map[string]any{"type": "local", "tag": tag, "format": "binary", "path": filepath.Join(dir, tag+".srs")}
}

// ConnInfo describes the live connection for the UI.
type ConnInfo struct {
	Country     string `json:"country"`
	CountryName string `json:"country_name"`
	Flag        string `json:"flag"`
	Target      string `json:"target"`
	Server      string `json:"server"`
	Proto       string `json:"proto"`
	ExitIP      string `json:"exit_ip"`
	Delay       int    `json:"delay"`
	Since       int64  `json:"since"`
	Up          int64  `json:"up"`
	Down        int64  `json:"down"`
	UpRate      int64  `json:"up_rate"`
	DownRate    int64  `json:"down_rate"`
	Group       int    `json:"group"`
	Mode        string `json:"mode"`
	ProxyAddr   string `json:"proxy_addr"`
}

func (e *Engine) connInfo() *ConnInfo {
	e.mu.Lock()
	c := e.conn
	e.mu.Unlock()
	if c == nil {
		return nil
	}
	c.mu.Lock()
	info := &ConnInfo{
		Country: c.exitCC, Target: c.country, ExitIP: c.exitIP, Delay: c.delay, Since: c.started.Unix(),
		Up: c.up, Down: c.down, UpRate: c.upRate, DownRate: c.dnRate, Group: len(c.servers), Mode: c.mode,
		ProxyAddr: c.proxyAddr,
	}
	active := c.active
	c.mu.Unlock()
	if info.Country == "" {
		info.Country = c.country
	}
	info.CountryName = countryName(info.Country)
	info.Flag = geo.Flag(info.Country)
	for _, s := range c.servers {
		if s.Key == active {
			info.Server, info.Proto = s.Name, s.Proto
		}
	}
	if info.Server == "" && len(c.servers) > 0 {
		// urltest starts on the first (fastest known) server.
		info.Server, info.Proto = c.servers[0].Name, c.servers[0].Proto
	}
	return info
}
