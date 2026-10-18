// Package engine finds working servers continuously and keeps the user
// connected to the fastest ones in the country they picked.
package engine

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/cezomagoanlace29861-jpg/vp/internal/antiblock"
	"github.com/cezomagoanlace29861-jpg/vp/internal/core"
	"github.com/cezomagoanlace29861-jpg/vp/internal/geo"
	"github.com/cezomagoanlace29861-jpg/vp/internal/pool"
	"github.com/cezomagoanlace29861-jpg/vp/internal/sources"
	"github.com/cezomagoanlace29861-jpg/vp/internal/tester"
)

// Options configure a new engine.
type Options struct {
	DataDir  string // settings, caches and logs
	AppDirs  []string
	ExeName  string // this app's executable name, routed direct in TUN mode
	IsAdmin  func() bool
	SysProxy SystemProxy
	Version  string
	// Autostart turns launching at login on or off.
	Autostart func(enabled bool) error
	// Sources replaces the built-in source list (tests use it).
	Sources []sources.Source
}

// SystemProxy sets and clears the OS proxy settings.
type SystemProxy interface {
	Set(addr string, bypass []string) error
	Clear() error
	// Owned reports whether the OS proxy currently points at addr, which
	// means a previous run crashed without cleaning up.
	Owned(addr string) bool
}

// Engine is the app's brain. All exported methods are safe to call from
// any goroutine.
type Engine struct {
	opt  Options
	dir  string
	bin  core.Binaries
	rt   *core.Runtime
	pool *pool.Pool

	fetcher  *sources.Fetcher
	tester   *tester.Tester
	resolver *antiblock.Resolver
	cf       *antiblock.Scanner

	mu           sync.Mutex
	settings     Settings
	srcs         []sources.Source
	srcStats     []SourceStat
	srcStatsByID map[string]SourceStat
	srcIndex     map[string]int
	good         map[uint64]*Server
	conn         *connection
	status       string
	message      string
	target       string // country the user wants ("" = fastest anywhere)
	wantConnect  bool   // keep trying to connect until a server is found
	scan         scanState
	hintCounts   map[string]int
	logs         []LogLine

	ctx    context.Context
	cancel context.CancelFunc
	wake   chan struct{}
	refill chan struct{}
	wg     sync.WaitGroup
}

// LogLine is one entry of the activity log shown in the UI.
type LogLine struct {
	Time  time.Time `json:"time"`
	Level string    `json:"level"`
	Text  string    `json:"text"`
}

// Status values.
const (
	StatusIdle         = "idle"
	StatusSearching    = "searching"
	StatusConnecting   = "connecting"
	StatusConnected    = "connected"
	StatusReconnecting = "reconnecting"
)

// New prepares an engine; Start begins work.
func New(opt Options) (*Engine, error) {
	if err := os.MkdirAll(opt.DataDir, 0o755); err != nil {
		return nil, err
	}
	for _, d := range []string{"cache", "run", "rules"} {
		os.MkdirAll(filepath.Join(opt.DataDir, d), 0o755)
	}
	if opt.IsAdmin == nil {
		opt.IsAdmin = func() bool { return false }
	}
	if opt.SysProxy == nil {
		opt.SysProxy = noProxy{}
	}
	e := &Engine{
		opt:          opt,
		dir:          opt.DataDir,
		good:         map[uint64]*Server{},
		srcStatsByID: map[string]SourceStat{},
		status:       StatusIdle,
		wake:         make(chan struct{}, 1),
		refill:       make(chan struct{}, 1),
		hintCounts:   map[string]int{},
	}
	e.settings = loadSettings(e.dir)
	e.target = e.settings.Country
	e.bin = core.Locate(append(opt.AppDirs, e.dir)...)
	e.rt = &core.Runtime{Bin: e.bin, Dir: filepath.Join(e.dir, "run")}
	p, err := pool.Open(filepath.Join(e.dir, "pool.dat"))
	if err != nil {
		return nil, err
	}
	e.pool = p
	direct := &http.Client{Transport: &http.Transport{
		Proxy:               nil,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout: 15 * time.Second,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     30 * time.Second,
	}}
	e.fetcher = &sources.Fetcher{Direct: direct, Proxy: e.proxyClient, CacheDir: filepath.Join(e.dir, "cache")}
	e.tester = &tester.Tester{Runtime: e.rt}
	e.resolver = &antiblock.Resolver{Client: e.anyClient}
	e.cf = &antiblock.Scanner{}
	e.applyIntensity()
	if err := installRules(filepath.Join(e.dir, "rules")); err != nil {
		e.logf("warn", "نصب فهرست سایت‌های ایرانی ناموفق بود: %v", err)
	}
	e.loadServers()
	e.loadSourceStats()
	e.buildSources()
	return e, nil
}

// Start begins scanning (and connects if the user asked for that).
func (e *Engine) Start() {
	e.ctx, e.cancel = context.WithCancel(context.Background())
	addr := e.proxyAddr()
	if e.opt.SysProxy.Owned(addr) {
		// A crash left the system pointing at our dead proxy.
		e.opt.SysProxy.Clear()
	}
	if e.bin.SingBox == "" {
		e.logf("error", "هسته sing-box پیدا نشد؛ برنامه را دوباره نصب کنید.")
	}
	if e.bin.Xray == "" {
		e.logf("warn", "هسته Xray پیدا نشد؛ سرورهای xhttp و چند نوع دیگر آزمایش نمی‌شوند.")
	}
	e.logf("info", "%d سرور سالم از دفعه قبل بارگذاری شد.", len(e.good))
	e.wg.Add(3)
	go e.refreshLoop()
	go e.scanLoop()
	go e.statsLoop()
	e.mu.Lock()
	auto := e.settings.AutoConnect
	e.mu.Unlock()
	if auto {
		go e.Connect(e.target)
	}
}

// Close disconnects, stops all work and saves state.
func (e *Engine) Close() {
	e.Disconnect()
	if e.cancel != nil {
		e.cancel()
	}
	e.wg.Wait()
	e.saveServers()
	e.saveSourceStats()
	e.saveStates()
	e.pool.Close()
}

func (e *Engine) logf(level, format string, args ...any) {
	line := LogLine{Time: time.Now(), Level: level, Text: fmt.Sprintf(format, args...)}
	e.mu.Lock()
	e.logs = append(e.logs, line)
	if len(e.logs) > 400 {
		e.logs = e.logs[len(e.logs)-400:]
	}
	e.mu.Unlock()
}

// Logs returns recent activity.
func (e *Engine) Logs() []LogLine {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]LogLine(nil), e.logs...)
}

func (e *Engine) setStatus(status, message string) {
	e.mu.Lock()
	e.status, e.message = status, message
	e.mu.Unlock()
}

func (e *Engine) poke() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *Engine) proxyAddr() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return "127.0.0.1:" + strconv.Itoa(e.settings.ProxyPort)
}

// proxyClient returns an HTTP client through our own proxy while
// connected, so blocked sources (Telegram, GitHub) are fetched through it.
func (e *Engine) proxyClient() *http.Client {
	e.mu.Lock()
	c := e.conn
	e.mu.Unlock()
	if c == nil || !c.ready() {
		return nil
	}
	u := &url.URL{Scheme: "http", Host: c.proxyAddr}
	return &http.Client{Transport: &http.Transport{
		Proxy:               http.ProxyURL(u),
		TLSHandshakeTimeout: 20 * time.Second,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     30 * time.Second,
	}}
}

func (e *Engine) anyClient() *http.Client {
	if c := e.proxyClient(); c != nil {
		return c
	}
	return e.fetcher.Direct
}

func (e *Engine) applyIntensity() {
	switch e.settings.Intensity {
	case "low":
		e.tester.Concurrency, e.tester.TCPConcurrency = 12, 48
	case "max":
		e.tester.Concurrency, e.tester.TCPConcurrency = 64, 256
	default:
		e.tester.Concurrency, e.tester.TCPConcurrency = 32, 128
	}
	e.tester.Timeout = 10 * time.Second
	e.tester.TCPTimeout = 3 * time.Second
}

// Settings returns the current settings.
func (e *Engine) Settings() Settings {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.settings
}

// UpdateSettings applies new settings and reconnects when routing changed.
func (e *Engine) UpdateSettings(s Settings) error {
	e.mu.Lock()
	old := e.settings
	if s.ProxyPort <= 0 || s.ProxyPort > 65535 {
		s.ProxyPort = old.ProxyPort
	}
	s.Country = e.target
	e.settings = s
	e.applyIntensity()
	connected := e.conn != nil
	e.mu.Unlock()
	if s.Mode == "tun" && old.Mode != "tun" && !e.opt.IsAdmin() {
		e.mu.Lock()
		e.settings.Mode = old.Mode
		e.mu.Unlock()
		return ErrNeedAdmin
	}
	saveJSON(filepath.Join(e.dir, "settings.json"), e.Settings())
	if old.DeepSources != s.DeepSources || len(old.UserSources) != len(s.UserSources) || len(old.DisabledSources) != len(s.DisabledSources) {
		e.buildSources()
		e.triggerRefill()
	}
	routing := old.Mode != s.Mode || old.BypassIran != s.BypassIran || old.BlockAds != s.BlockAds ||
		old.ProxyPort != s.ProxyPort || old.AllowLAN != s.AllowLAN || old.Fragment != s.Fragment || old.AllowPlain != s.AllowPlain
	if connected && routing {
		go e.Connect(e.target)
	}
	if e.opt.Autostart != nil && old.StartWithWindows != s.StartWithWindows {
		if err := e.opt.Autostart(s.StartWithWindows); err != nil {
			e.logf("warn", "تنظیم اجرای خودکار ناموفق بود: %v", err)
		}
	}
	return nil
}

// ErrNeedAdmin means TUN mode needs the app to run as administrator.
var ErrNeedAdmin = fmt.Errorf("need administrator")

func (e *Engine) triggerRefill() {
	select {
	case e.refill <- struct{}{}:
	default:
	}
}

// buildSources rebuilds the source list from built-ins and settings.
func (e *Engine) buildSources() {
	e.mu.Lock()
	defer e.mu.Unlock()
	disabled := map[string]bool{}
	for _, id := range e.settings.DisabledSources {
		disabled[id] = true
	}
	var list []sources.Source
	builtin := e.opt.Sources
	if builtin == nil {
		builtin = sources.Builtin()
	}
	for _, s := range builtin {
		if s.Tier == sources.TierDeep && !e.settings.DeepSources {
			s.Enabled = false
		}
		if disabled[s.ID] {
			s.Enabled = false
		}
		list = append(list, s)
	}
	list = append(list, e.settings.UserSources...)
	// Keep indexes stable for sources that already exist: the pool stores
	// source indexes.
	if e.srcIndex == nil {
		e.srcIndex = map[string]int{}
	}
	byID := map[string]sources.Source{}
	for _, s := range list {
		byID[s.ID] = s
	}
	for i := range e.srcs {
		if s, ok := byID[e.srcs[i].ID]; ok {
			e.srcs[i] = s
			delete(byID, s.ID)
		} else {
			e.srcs[i].Enabled = false
		}
	}
	for _, s := range list {
		if _, isNew := byID[s.ID]; !isNew {
			continue
		}
		e.srcIndex[s.ID] = len(e.srcs)
		e.srcs = append(e.srcs, s)
		e.srcStats = append(e.srcStats, e.srcStatsByID[s.ID])
	}
}

// SourceInfo describes a source for the UI.
type SourceInfo struct {
	sources.Source
	Label string `json:"label"`
	SourceStat
}

// Sources lists every source with its statistics.
func (e *Engine) Sources() []SourceInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]SourceInfo, 0, len(e.srcs))
	for i, s := range e.srcs {
		if !s.Builtin && !containsSource(e.settings.UserSources, s.ID) {
			continue
		}
		out = append(out, SourceInfo{Source: s, Label: s.Label(), SourceStat: e.srcStats[i]})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].OK > out[j].OK })
	return out
}

func containsSource(list []sources.Source, id string) bool {
	for _, s := range list {
		if s.ID == id {
			return true
		}
	}
	return false
}

// AddSource adds a user subscription URL or Telegram channel.
func (e *Engine) AddSource(input string) (sources.Source, error) {
	s, ok := sources.User(input)
	if !ok {
		return s, fmt.Errorf("invalid source")
	}
	set := e.Settings()
	if containsSource(set.UserSources, s.ID) {
		return s, nil
	}
	set.UserSources = append(set.UserSources, s)
	if err := e.UpdateSettings(set); err != nil {
		return s, err
	}
	return s, nil
}

// RemoveSource deletes a user source or disables a built-in one.
func (e *Engine) RemoveSource(id string) {
	set := e.Settings()
	var keep []sources.Source
	for _, s := range set.UserSources {
		if s.ID != id {
			keep = append(keep, s)
		}
	}
	if len(keep) == len(set.UserSources) {
		set.DisabledSources = append(set.DisabledSources, id)
	}
	set.UserSources = keep
	e.UpdateSettings(set)
}

// EnableSource re-enables a built-in source.
func (e *Engine) EnableSource(id string) {
	set := e.Settings()
	var keep []string
	for _, d := range set.DisabledSources {
		if d != id {
			keep = append(keep, d)
		}
	}
	set.DisabledSources = keep
	e.UpdateSettings(set)
}

// CountryInfo summarizes servers in one country.
type CountryInfo struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	NameEN     string `json:"name_en"`
	Flag       string `json:"flag"`
	Servers    int    `json:"servers"`
	BestDelay  int    `json:"best_delay"`
	Candidates int    `json:"candidates"`
}

// Countries lists countries with working servers first, then countries
// that only have untested candidates.
func (e *Engine) Countries() []CountryInfo {
	now := time.Now()
	e.mu.Lock()
	m := map[string]*CountryInfo{}
	get := func(code string) *CountryInfo {
		ci, ok := m[code]
		if !ok {
			c, _ := geo.Lookup(code)
			ci = &CountryInfo{Code: code, Name: c.FA, NameEN: c.EN, Flag: geo.Flag(code)}
			m[code] = ci
		}
		return ci
	}
	for _, s := range e.good {
		if s.Country == "" || s.Country == "IR" || !s.usable(now) {
			continue
		}
		ci := get(s.Country)
		ci.Servers++
		if ci.BestDelay == 0 || s.Delay < ci.BestDelay {
			ci.BestDelay = s.Delay
		}
	}
	for code, n := range e.hintCounts {
		if code == "IR" || len(code) != 2 {
			continue
		}
		if _, known := geo.Lookup(code); !known {
			continue
		}
		get(code).Candidates = n
	}
	e.mu.Unlock()
	out := make([]CountryInfo, 0, len(m))
	for _, c := range m {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Servers > 0) != (out[j].Servers > 0) {
			return out[i].Servers > 0
		}
		if out[i].Servers != out[j].Servers {
			return out[i].Servers > out[j].Servers
		}
		return out[i].Candidates > out[j].Candidates
	})
	return out
}

// ServerInfo is a working server as shown in the UI.
type ServerInfo struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Proto     string `json:"proto"`
	Transport string `json:"transport"`
	Security  string `json:"security"`
	Country   string `json:"country"`
	Delay     int    `json:"delay"`
	AgeSec    int    `json:"age_sec"`
	Fragment  bool   `json:"fragment"`
	Plain     bool   `json:"plain"`
	Active    bool   `json:"active"`
	Link      string `json:"link"`
}

// Servers lists usable servers in a country ("" = all), best first.
func (e *Engine) Servers(country string, limit int) []ServerInfo {
	now := time.Now()
	e.mu.Lock()
	active := map[string]bool{}
	if e.conn != nil {
		active[e.conn.activeKey()] = true
	}
	var list []*Server
	for _, s := range e.good {
		if !s.usable(now) || (country != "" && s.Country != country) {
			continue
		}
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].rank(now) < list[j].rank(now) })
	if limit > 0 && len(list) > limit {
		list = list[:limit]
	}
	out := make([]ServerInfo, 0, len(list))
	for _, s := range list {
		out = append(out, ServerInfo{
			Key: s.Key, Name: s.Name, Proto: s.Proto, Transport: s.Transport, Security: s.Security,
			Country: s.Country, Delay: s.Delay, AgeSec: int(now.Sub(s.LastOK).Seconds()),
			Fragment: s.Fragment, Plain: s.Plain, Active: active[s.Key], Link: s.Link,
		})
	}
	e.mu.Unlock()
	return out
}
