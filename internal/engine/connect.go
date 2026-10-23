package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
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

// groupSize is how many servers one connection keeps ready: the one in use
// and its backups, all from the same country. They run in one core behind a
// selector, so moving to a backup is a change of selection, not a restart.
const groupSize = 8

// minReady is how many members of a group should be known to work. Below
// that the group is refreshed with new servers from the same country the
// next time the connection is idle.
const minReady = 3

type connection struct {
	country     string // what the user asked for ("" = fastest anywhere)
	home        string // country the group was built in; backups stay here
	pinned      bool   // the user picked this one server
	bypass      string // region whose sites go direct ("" = none)
	mode        string
	proxyAddr   string
	running     *core.Running
	servers     []*Server
	members     []*member
	clashAddr   string
	clashSecret string
	probeAddr   string // reaches one member by name, for speed tests
	started     time.Time
	sysProxy    bool
	cancel      context.CancelFunc
	wake        chan struct{} // asks the monitor to check the active server now

	mu       sync.Mutex
	active   *member
	delay    int
	exitIP   string
	exitCC   string
	up       int64
	down     int64
	upRate   int64
	dnRate   int64
	lastBusy time.Time // last second that carried traffic
	waiting  time.Time // when data went out with nothing back since
	healthy  bool
	switched time.Time // last time the member in use changed
	speedAt  time.Time // last speed round
	slowRuns int       // speed rounds in a row that found a much faster backup
	errs     []logErr  // recent failed connections, for watchLog
}

// member is one server of a live group.
type member struct {
	tag      string // outbound tag in the core
	server   *Server
	country  string // exit country when the group was built
	endpoint string // host:port to dial for an offline check ("" for UDP)

	// Guarded by connection.mu.
	alive   bool
	delay   int
	fails   int // failed checks in a row
	checked time.Time
	speed   float64 // download bytes per second at the last speed test
	speedAt time.Time
}

func (c *connection) ready() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.healthy
}

func (c *connection) activeMember() *member {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active
}

func (c *connection) activeKey() string {
	if m := c.activeMember(); m != nil {
		return m.server.Key
	}
	return ""
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
	if e.updateRequired() {
		return ErrUpdateRequired
	}
	if err := e.AllowConnect(); err != nil {
		var limit *LimitError
		if errors.As(err, &limit) {
			e.setStatus(StatusIdle, limit.Message)
		}
		return err
	}
	country = strings.ToUpper(strings.TrimSpace(country))
	e.mu.Lock()
	e.target = country
	e.settings.Country = country
	e.settings.Connected = true
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
	return e.connectLoop(gen, country, "", nil, nil)
}

// ConnectServer connects to one specific working server.
func (e *Engine) ConnectServer(key string) error {
	if e.updateRequired() {
		return ErrUpdateRequired
	}
	if err := e.AllowConnect(); err != nil {
		return err
	}
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
	e.settings.Connected = true
	e.wantConnect = true
	e.connGen++
	gen := e.connGen
	e.mu.Unlock()
	saveJSON(filepath.Join(e.dir, "settings.json"), e.Settings())
	return e.connectLoop(gen, s.Country, "", []*Server{s}, nil)
}

func countryName(code string) string {
	if code == "" {
		return "Fastest location"
	}
	c, _ := geo.Lookup(code)
	return c.FA
}

// connectLoop keeps trying until a group in country is up. home keeps a
// fastest-anywhere connection in the country it was already using; avoid
// lists servers that just failed.
func (e *Engine) connectLoop(gen int, country, home string, pinned []*Server, avoid []string) error {
	name := countryName(country)
	deadline := time.Now().Add(3 * time.Minute)
	exclude := map[string]bool{}
	for _, k := range avoid {
		exclude[k] = true
	}
	triedRecent := false
	for attempt := 0; ; attempt++ {
		e.mu.Lock()
		want := e.wantConnect && e.target == country && e.connGen == gen
		e.mu.Unlock()
		if !want || e.ctx.Err() != nil {
			return errors.New("cancelled")
		}
		picks := pinned
		if picks == nil {
			picks = e.pickGroup(country, home, exclude)
		}
		if len(picks) == 0 && !triedRecent {
			// Nothing fresh: try last week's servers once before waiting on
			// the scan.
			triedRecent = true
			picks = e.recentServers(country, groupSize, exclude)
		}
		if len(picks) == 0 {
			if len(exclude) > 0 {
				// Everything usable has been tried once; try again after the
				// pause. Servers that keep failing drop out on their own.
				exclude = map[string]bool{}
			}
			e.setStatus(StatusSearching, fmt.Sprintf("Looking for a server in %s…", name))
			if time.Now().After(deadline) {
				e.setStatus(StatusSearching, fmt.Sprintf("No server in %s yet. Still looking; you will be connected as soon as one is found.", name))
			}
			select {
			case <-e.ctx.Done():
				return e.ctx.Err()
			case <-time.After(2 * time.Second):
			}
			continue
		}
		e.setStatus(StatusConnecting, fmt.Sprintf("Connecting to %s…", name))
		err := e.start(gen, country, picks, pinned != nil)
		if err == nil {
			return nil
		}
		if errors.Is(err, errStale) {
			return err
		}
		e.logf("warn", "Connection attempt failed: %v", err)
		for _, s := range picks {
			exclude[s.Key] = true
		}
		if pinned != nil {
			e.setStatus(StatusIdle, "This server is not available right now.")
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

// pickGroup chooses the servers for a connection: the fastest in country
// with backups from the same country. A fastest-anywhere group forms around
// the best server's country (or home, when it already had one) and reaches
// into other countries only when that country runs short.
func (e *Engine) pickGroup(country, home string, exclude map[string]bool) []*Server {
	if country != "" {
		return e.bestServers(country, groupSize, exclude)
	}
	if home == "" {
		best := e.bestServers("", 1, exclude)
		if len(best) == 0 {
			return nil
		}
		home = best[0].Country
	}
	group := e.bestServers(home, groupSize, exclude)
	if len(group) >= minReady {
		return group
	}
	skip := map[string]bool{}
	for k := range exclude {
		skip[k] = true
	}
	for _, s := range group {
		skip[s.Key] = true
	}
	return append(group, e.bestServers("", groupSize-len(group), skip)...)
}

// bestServers picks the lowest-delay usable servers in a country.
func (e *Engine) bestServers(country string, n int, exclude map[string]bool) []*Server {
	return e.rankedServers(country, n, exclude, (*Server).usable)
}

// recentServers picks servers that worked in the last week but are no
// longer counted usable. When nothing fresh is known (the app was closed
// for a while, or filtering got heavier), trying them at once is much
// faster than waiting for a scan, and most still work.
func (e *Engine) recentServers(country string, n int, exclude map[string]bool) []*Server {
	return e.rankedServers(country, n, exclude, func(s *Server, now time.Time) bool {
		return !s.usable(now) && s.Fails < 6 && now.Sub(s.LastOK) < 7*24*time.Hour
	})
}

func (e *Engine) rankedServers(country string, n int, exclude map[string]bool, ok func(*Server, time.Time) bool) []*Server {
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	var list []*Server
	for _, s := range e.good {
		if exclude[s.Key] || !ok(s, now) || s.Country == "IR" || !s.matches(e.settings.Protocol) {
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
			if warpPlus >= 1 {
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
func (e *Engine) start(gen int, country string, servers []*Server, pinned bool) error {
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
			e.logf("warn", "Port %d is used by another program; using port %d instead.", proxyPort, p[0])
			proxyPort = p[0]
		}
	}
	ports, err := core.FreePorts(2)
	if err != nil {
		return err
	}
	home := country
	if home == "" {
		home = servers[0].Country
	}
	c := &connection{
		country:     country,
		home:        home,
		pinned:      pinned,
		bypass:      set.Bypass,
		mode:        set.Mode,
		proxyAddr:   "127.0.0.1:" + strconv.Itoa(proxyPort),
		clashAddr:   "127.0.0.1:" + strconv.Itoa(ports[0]),
		probeAddr:   "127.0.0.1:" + strconv.Itoa(ports[1]),
		clashSecret: randomToken(),
		started:     time.Now(),
		wake:        make(chan struct{}, 1),
	}
	opts := connOptions{
		listen: listen, port: proxyPort, tun: set.Mode == "tun", bypass: set.Bypass, secureOnly: set.SecureOnly,
		blockAds: set.BlockAds, rulesDir: filepath.Join(e.dir, "rules"), clashAddr: c.clashAddr,
		clashSecret: c.clashSecret, probeAddr: c.probeAddr, exe: e.opt.ExeName, localDNS: e.dns(), mobile: e.opt.Mobile,
	}
	build := core.BuildOptions{
		Fragment:    set.Fragment == "on",
		FragmentFor: func(n *node.Node) bool { return fragFor[n] },
		OnLine:      c.watchLog,
	}
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
	// The API that switches servers can come up a moment after the proxy
	// port does.
	if err := run.SingBox.WaitListening(ctx, c.clashAddr, 5*time.Second); err != nil {
		run.Stop()
		return err
	}
	e.mu.Lock()
	for i, n := range run.Set.Nodes {
		if s := byNode[n]; s != nil {
			m := &member{tag: run.Set.Tags[i], server: s, country: s.Country}
			if !n.UDPBased() && n.Proto != "warpplus" {
				m.endpoint = n.Endpoint()
			}
			c.members = append(c.members, m)
			c.servers = append(c.servers, s)
		}
	}
	e.mu.Unlock()
	// Test the whole group at once and start on the first server to answer;
	// the rest keep being tested and become the known-good backups.
	proxy := &url.URL{Scheme: "http", Host: c.proxyAddr}
	var r tester.Result
	tried := map[*member]bool{}
	for try := 0; try < 3 && !r.OK; try++ {
		var rest []*member
		for _, m := range c.members {
			if !tried[m] {
				rest = append(rest, m)
			}
		}
		m := e.race(e.ctx, c, rest, 8*time.Second)
		if m == nil {
			break
		}
		tried[m] = true
		if err := c.use(e.ctx, m); err != nil {
			run.Stop()
			return err
		}
		// Prove the whole path works before telling anyone it does.
		r = tester.Probe(e.ctx, proxy, 12*time.Second)
		if !r.OK {
			e.noteResult(c, m, 0, false)
		}
	}
	if !r.OK {
		run.Stop()
		if len(tried) == 0 {
			return fmt.Errorf("%w: no server in the group answered", errServers)
		}
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
		if r, ok := findRegion(set.Bypass); ok {
			for _, s := range r.Suffixes {
				bypass = append(bypass, "*"+s)
			}
		}
		switch err := e.setProxy(gen, c.proxyAddr, bypass); {
		case errors.Is(err, errStale):
			run.Stop()
			return err
		case err != nil:
			e.logf("warn", "Could not set the system proxy: %v", err)
		default:
			c.sysProxy = true
		}
	}
	mctx, mcancel := context.WithCancel(e.ctx)
	c.cancel = mcancel
	e.mu.Lock()
	if e.connGen != gen {
		// The user disconnected while this connection was being made.
		e.mu.Unlock()
		e.stopConn(c, true)
		return errStale
	}
	e.conn = c
	e.mu.Unlock()
	e.setStatus(StatusConnected, connectedMessage(c))
	e.logf("info", "Connected to %s with %d servers in the group (latency %d ms).", exitName(c), len(c.members), c.delay)
	go e.monitor(mctx, c)
	// Sources blocked on the direct path (Telegram, GitHub) can now be
	// read through the VPN.
	e.triggerRefill()
	return nil
}

// exitName is the country traffic leaves from, as the user sees it.
func exitName(c *connection) string {
	c.mu.Lock()
	cc := c.exitCC
	c.mu.Unlock()
	if cc == "" {
		cc = c.country
	}
	return countryName(cc)
}

func connectedMessage(c *connection) string {
	return fmt.Sprintf("Connected — %s", exitName(c))
}

// Disconnect stops the VPN and restores system settings.
func (e *Engine) Disconnect() {
	e.disconnect(true)
}

// disconnect takes the connection and the VPN down; byUser also records
// that the user wants it off (see Settings.Connected).
func (e *Engine) disconnect(byUser bool) {
	e.mu.Lock()
	e.wantConnect = false
	e.connGen++
	c := e.conn
	wasOn := e.settings.Connected
	if byUser {
		e.settings.Connected = false
	}
	e.mu.Unlock()
	if byUser && wasOn {
		saveJSON(filepath.Join(e.dir, "settings.json"), e.Settings())
	}
	if c != nil {
		e.stopConn(c, true)
	}
	// Between a dropped connection and its replacement there is no
	// connection to stop, but the VPN (the system proxy on Windows) is
	// still up; it must go too.
	e.clearProxy()
	e.setStatus(StatusIdle, "")
}

// Resume connects again when the user left the VPN on, for Android's
// Always-on VPN starting the app by itself (after a restart, say). It
// reports whether a connection is on its way.
func (e *Engine) Resume() bool {
	e.mu.Lock()
	on, busy, target := e.settings.Connected, e.wantConnect, e.target
	e.mu.Unlock()
	if !on {
		return false
	}
	if !busy {
		go e.Connect(target)
	}
	return true
}

// setProxy points the system (on Android, the VPN) at addr unless the user
// disconnected since connection attempt gen began. proxyMu makes the check
// and the change one step, so a disconnect cannot slip in between.
func (e *Engine) setProxy(gen int, addr string, bypass []string) error {
	e.proxyMu.Lock()
	defer e.proxyMu.Unlock()
	e.mu.Lock()
	stale := e.connGen != gen
	e.mu.Unlock()
	if stale {
		return errStale
	}
	if err := e.opt.SysProxy.Set(addr, bypass); err != nil {
		return err
	}
	e.proxyOn = true
	return nil
}

// clearProxy undoes setProxy, if it is in effect.
func (e *Engine) clearProxy() {
	e.proxyMu.Lock()
	defer e.proxyMu.Unlock()
	if e.proxyOn {
		e.opt.SysProxy.Clear()
		e.proxyOn = false
	}
}

func (e *Engine) stopConn(c *connection, clearProxy bool) {
	if c.cancel != nil {
		c.cancel()
	}
	if c.sysProxy && clearProxy {
		e.clearProxy()
	}
	c.running.Stop()
	e.mu.Lock()
	if e.conn == c {
		e.conn = nil
	}
	e.mu.Unlock()
}

type connOptions struct {
	listen      string
	port        int
	tun         bool
	bypass      string
	secureOnly  bool
	blockAds    bool
	rulesDir    string
	clashAddr   string
	clashSecret string
	probeAddr   string
	exe         string
	localDNS    string
	mobile      bool
}

// connectConfig builds the sing-box config for a live connection.
func connectConfig(s *core.Set, o connOptions) map[string]any {
	outbounds := append([]any{}, s.Outbounds...)
	outbounds = append(outbounds,
		// The engine picks the member in use and moves to a backup itself
		// (see failover.go), so a switch takes effect at once.
		map[string]any{
			"type": "selector", "tag": "proxy", "outbounds": s.Tags, "default": s.Tags[0],
			"interrupt_exist_connections": false,
		},
		map[string]any{"type": "direct", "tag": "direct"},
	)
	inbounds := []any{map[string]any{"type": "mixed", "tag": "mixed-in", "listen": o.listen, "listen_port": o.port}}
	var rules []any
	if o.probeAddr != "" {
		// The engine measures any member's speed through this inbound by
		// naming it as the user, without moving the user's traffic.
		host, port, _ := net.SplitHostPort(o.probeAddr)
		p, _ := strconv.Atoi(port)
		users := make([]any, 0, len(s.Tags))
		for _, tag := range s.Tags {
			users = append(users, map[string]any{"username": tag, "password": o.clashSecret})
			rules = append(rules, map[string]any{"inbound": []string{"probe-in"}, "auth_user": []string{tag}, "outbound": tag})
		}
		rules = append(rules, map[string]any{"inbound": []string{"probe-in"}, "action": "reject"})
		inbounds = append(inbounds, map[string]any{"type": "mixed", "tag": "probe-in", "listen": host, "listen_port": p, "users": users})
	}
	if o.tun {
		inbounds = append(inbounds, map[string]any{
			"type": "tun", "tag": "tun-in", "interface_name": "Albatross",
			"address": []string{"172.19.0.1/30", "fdfe:dcba:9876::1/126"},
			"mtu":     9000, "auto_route": true, "strict_route": true, "stack": "mixed",
		})
	}
	var ruleSets []any
	rules = append(rules,
		map[string]any{"action": "sniff"},
		map[string]any{"protocol": "dns", "action": "hijack-dns"},
		map[string]any{"ip_is_private": true, "outbound": "direct"},
	)
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
	if r, ok := findRegion(o.bypass); ok {
		// The learned list holds the region's sites the domain list misses
		// (learn.go); the core reloads it whenever the engine adds one.
		ruleSets = append(ruleSets, localRuleSet(r.geoSiteTag(), o.rulesDir), localRuleSet(r.geoIPTag(), o.rulesDir),
			map[string]any{"type": "local", "tag": r.learnedTag(), "format": "source", "path": r.learnedFile(o.rulesDir)})
		rules = append(rules, map[string]any{
			"domain_suffix": r.Suffixes,
			"rule_set":      []string{r.geoSiteTag(), r.learnedTag(), r.geoIPTag()},
			"outbound":      "direct",
		})
		dnsRules = append(dnsRules, map[string]any{
			"domain_suffix": r.Suffixes, "rule_set": []string{r.geoSiteTag(), r.learnedTag()},
			"action": "route", "server": "local",
		})
	}
	if o.secureOnly {
		// Last among the rules that send traffic elsewhere, so local sites
		// and the local network (which never reach a server) are untouched.
		// HTTP is recognised by its content on any port, the rest by the
		// ports of plaintext protocols: FTP, telnet, SMTP, HTTP, POP3, IMAP.
		rules = append(rules,
			map[string]any{"protocol": []string{"http"}, "action": "reject"},
			map[string]any{"network": "tcp", "port": []int{21, 23, 25, 80, 110, 143}, "action": "reject"},
		)
	}
	route := map[string]any{
		"rules": rules,
		"final": "proxy",
		// Never on Android: see tester.Tester.Mobile.
		"auto_detect_interface":   !o.mobile,
		"default_domain_resolver": "local",
	}
	if len(ruleSets) > 0 {
		route["rule_set"] = ruleSets
	}
	dns := map[string]any{
		"servers": []any{
			tester.DNSServer(o.localDNS),
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
	Ready       int    `json:"ready"` // backups known to work right now
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
		Up: c.up, Down: c.down, UpRate: c.upRate, DownRate: c.dnRate, Group: len(c.members), Mode: c.mode,
		ProxyAddr: c.proxyAddr,
	}
	active := c.active
	for _, m := range c.members {
		if m.alive && m != active {
			info.Ready++
		}
	}
	c.mu.Unlock()
	if info.Country == "" {
		info.Country = c.country
	}
	info.CountryName = countryName(info.Country)
	info.Flag = geo.Flag(info.Country)
	if active != nil {
		info.Server, info.Proto = active.server.Name, active.server.Proto
	} else if len(c.servers) > 0 {
		info.Server, info.Proto = c.servers[0].Name, c.servers[0].Proto
	}
	return info
}
