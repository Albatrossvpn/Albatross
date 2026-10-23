package core

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Tor is the last resort: it reaches the Tor network through bridges
// (Snowflake, WebTunnel, obfs4, meek), which keep working when every
// ordinary server is blocked. Bootstrapping takes from seconds (with the
// directory cached) to a couple of minutes, far too long to repeat for
// every core restart, so one tor process is shared by every set that uses
// it and stops only after none has for torIdle.

const (
	torIdle    = 10 * time.Minute
	torWait    = 2 * time.Minute
	torPTFile  = "pt_config.json"
	torPTDir   = "pluggable_transports"
	torDoneMsg = "Bootstrapped 100%"
)

// torOrder is the order bridges are written in; tor tries them all, and
// the transports that get through Iran's filtering most often come first.
var torOrder = []string{"snowflake", "webtunnel", "obfs4", "meek_lite", "meek", "conjure"}

type torProc struct {
	mu    sync.Mutex
	p     *Process
	port  int
	ready chan struct{}
	users int
	idle  *time.Timer
}

// ptConfig is Tor Browser's pt_config.json, which the Tor Expert Bundle
// ships next to its transports.
type ptConfig struct {
	PluggableTransports map[string]string   `json:"pluggableTransports"`
	Bridges             map[string][]string `json:"bridges"`
}

// acquireTor returns the SOCKS port of a bootstrapped tor, starting tor if
// needed. Each success must be paired with a releaseTor.
func (r *Runtime) acquireTor(ctx context.Context) (int, error) {
	t := &r.tor
	t.mu.Lock()
	if t.idle != nil {
		t.idle.Stop()
		t.idle = nil
	}
	if t.p == nil || t.p.Exited() {
		if err := r.startTorLocked(); err != nil {
			t.mu.Unlock()
			return 0, err
		}
	}
	t.users++
	p, ready, port := t.p, t.ready, t.port
	t.mu.Unlock()
	select {
	case <-ready:
		return port, nil
	case <-p.Done():
	case <-ctx.Done():
	case <-time.After(torWait):
	}
	// A slow bootstrap keeps going for the next attempt.
	r.releaseTor()
	return 0, fmt.Errorf("tor did not connect")
}

func (r *Runtime) releaseTor() {
	t := &r.tor
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.users > 0 {
		t.users--
	}
	if t.users > 0 || t.p == nil {
		return
	}
	p := t.p
	t.idle = time.AfterFunc(torIdle, func() {
		t.mu.Lock()
		stop := t.users == 0 && t.p == p
		if stop {
			t.p, t.idle = nil, nil
		}
		t.mu.Unlock()
		if stop {
			p.Stop()
		}
	})
}

// StopTor stops tor at once.
func (r *Runtime) StopTor() {
	t := &r.tor
	t.mu.Lock()
	p := t.p
	t.p = nil
	if t.idle != nil {
		t.idle.Stop()
		t.idle = nil
	}
	t.mu.Unlock()
	p.Stop()
}

func (r *Runtime) startTorLocked() error {
	if r.Bin.Tor == "" {
		return fmt.Errorf("tor is not installed")
	}
	dir := filepath.Join(r.Dir, "tor")
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o700); err != nil {
		return err
	}
	ports, err := FreePorts(1)
	if err != nil {
		return err
	}
	var extra []byte
	if r.TorBridges != nil {
		extra = r.TorBridges()
	}
	rc, err := TorConfig(r.Bin.Tor, ports[0], filepath.Join(dir, "data"), extra)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "torrc")
	if err := os.WriteFile(path, []byte(rc), 0o600); err != nil {
		return err
	}
	ready := make(chan struct{})
	var once sync.Once
	// The bundle's Linux and Android builds keep their libraries (libevent,
	// OpenSSL) beside tor.
	env := []string{"LD_LIBRARY_PATH=" + filepath.Dir(r.Bin.Tor)}
	p, err := Start("tor", r.Bin.Tor, []string{"-f", path}, env, func(l string) {
		if strings.Contains(l, torDoneMsg) {
			once.Do(func() { close(ready) })
		}
		if r.OnLine != nil {
			r.OnLine("tor", l)
		}
	})
	if err != nil {
		return err
	}
	r.tor.p, r.tor.port, r.tor.ready = p, ports[0], ready
	return nil
}

// TorConfig writes a torrc that uses every bridge whose transport is
// installed. extra is a JSON map of transport to bridge lines (the
// collector's copy of Tor's current built-in bridges), added to the ones
// the bundle came with.
func TorConfig(torBin string, port int, dataDir string, extra []byte) (string, error) {
	cfg, ptDir := loadPTConfig(torBin)
	if cfg == nil {
		return "", fmt.Errorf("no pluggable transports next to tor")
	}
	var more map[string][]string
	if len(extra) > 0 {
		json.Unmarshal(extra, &more)
	}
	var b strings.Builder
	line := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }
	line("SocksPort 127.0.0.1:%d", port)
	line("DataDirectory %s", quoteTorrc(dataDir))
	line("ClientOnly 1")
	line("AvoidDiskWrites 1")
	line("Log notice stdout")
	line("UseBridges 1")
	// tor exits by itself if the app dies without stopping it.
	line("__OwningControllerProcess %d", os.Getpid())
	geo := filepath.Join(filepath.Dir(filepath.Dir(torBin)), "data")
	if _, err := os.Stat(filepath.Join(geo, "geoip")); err == nil {
		line("GeoIPFile %s", quoteTorrc(filepath.Join(geo, "geoip")))
		line("GeoIPv6File %s", quoteTorrc(filepath.Join(geo, "geoip6")))
	}
	have := map[string]bool{}
	names := make([]string, 0, len(cfg.PluggableTransports))
	for k := range cfg.PluggableTransports {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		pl, ok := ptLine(cfg.PluggableTransports[k], ptDir, filepath.Dir(torBin))
		if !ok {
			continue
		}
		line("%s", pl)
		for _, t := range strings.Split(strings.Fields(pl)[1], ",") {
			have[t] = true
		}
	}
	seen := map[string]bool{}
	n := 0
	for _, key := range torKeys(cfg.Bridges, more) {
		for _, br := range append(append([]string(nil), more[key]...), cfg.Bridges[key]...) {
			br = strings.TrimSpace(br)
			f := strings.Fields(br)
			if len(f) < 2 || seen[br] || !have[f[0]] {
				continue
			}
			seen[br] = true
			line("Bridge %s", br)
			n++
		}
	}
	if n == 0 {
		return "", fmt.Errorf("no usable bridges")
	}
	return b.String(), nil
}

// torKeys orders the bridge groups by torOrder, then the rest by name.
func torKeys(a, b map[string][]string) []string {
	rank := func(k string) int {
		for i, o := range torOrder {
			if strings.HasPrefix(k, o) {
				return i
			}
		}
		return len(torOrder)
	}
	set := map[string]bool{}
	for k := range a {
		set[k] = true
	}
	for k := range b {
		set[k] = true
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if ri, rj := rank(keys[i]), rank(keys[j]); ri != rj {
			return ri < rj
		}
		return keys[i] < keys[j]
	})
	return keys
}

// embeddedPT is the Android bundle's pt_config.json, which
// build/android/build.sh copies here before building: Android installs
// only lib*.so files beside tor, so the file cannot ship there. The
// committed copy is empty.
//
//go:embed torpt/pt_config.json
var embeddedPT []byte

// loadPTConfig finds pt_config.json beside tor, as the Tor Expert Bundle
// lays it out (tor/pluggable_transports/) or flat in the same folder, or
// else the one built in, with the transports beside tor.
func loadPTConfig(torBin string) (*ptConfig, string) {
	d := filepath.Dir(torBin)
	for _, dir := range []string{filepath.Join(d, torPTDir), d, filepath.Join(filepath.Dir(d), torPTDir)} {
		b, err := os.ReadFile(filepath.Join(dir, torPTFile))
		if err != nil {
			continue
		}
		var c ptConfig
		if json.Unmarshal(b, &c) == nil && len(c.PluggableTransports) > 0 {
			return &c, dir
		}
	}
	var c ptConfig
	if json.Unmarshal(embeddedPT, &c) == nil && len(c.PluggableTransports) > 0 {
		return &c, d
	}
	return nil, ""
}

// ptLine turns one of pt_config.json's ClientTransportPlugin lines into a
// torrc line with the transport's real path, or reports that the
// transport is not installed. tor splits these lines at spaces and does
// not unquote them, so the path is written relative to tor's folder (tor
// runs there), which keeps spaces in the install path out of it.
func ptLine(s, dir, torDir string) (string, bool) {
	s = strings.ReplaceAll(s, "${pt_path}", dir+string(filepath.Separator))
	f := strings.Fields(s)
	// ClientTransportPlugin <names> exec <path> [args]
	if len(f) < 4 || f[0] != "ClientTransportPlugin" || f[2] != "exec" {
		return "", false
	}
	exe := f[3]
	if !filepath.IsAbs(exe) {
		exe = filepath.Join(dir, exe)
	}
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(exe), ".exe") {
		exe += ".exe"
	}
	if st, err := os.Stat(exe); err != nil || st.IsDir() {
		// Android ships executables as lib<name>.so (see Locate).
		exe = filepath.Join(filepath.Dir(exe), "lib"+strings.ReplaceAll(filepath.Base(exe), "-", "")+".so")
		if st, err := os.Stat(exe); err != nil || st.IsDir() {
			return "", false
		}
	}
	if rel, err := filepath.Rel(torDir, exe); err == nil && !strings.HasPrefix(rel, "..") {
		exe = rel
	}
	if strings.ContainsAny(exe, " \t\"") {
		return "", false
	}
	f[3] = exe
	return strings.Join(f, " "), true
}

// quoteTorrc quotes a path for torrc when it has spaces or backslashes.
func quoteTorrc(s string) string {
	if !strings.ContainsAny(s, " \\\"\t") {
		return s
	}
	return strconv.Quote(s)
}
