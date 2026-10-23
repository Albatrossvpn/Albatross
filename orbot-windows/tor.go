package main

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TorPaths describes where the Tor Expert Bundle lives next to the app.
type TorPaths struct {
	TorExe   string
	TorDir   string
	PTDir    string
	GeoIP    string
	GeoIPv6  string
	DataRoot string
}

func firstExisting(paths ...string) string {
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func firstDir(paths ...string) string {
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	return ""
}

func findTor(appDir, dataRoot string) (TorPaths, error) {
	exe := torExeName()
	p := TorPaths{DataRoot: dataRoot}
	p.TorExe = firstExisting(
		filepath.Join(appDir, "tor", exe),
		filepath.Join(appDir, exe),
		filepath.Join(appDir, "tor-bundle", "tor", exe),
	)
	if p.TorExe == "" {
		if lp, err := exec.LookPath(exe); err == nil {
			p.TorExe = lp
		}
	}
	if p.TorExe == "" {
		return p, errors.New("tor executable not found; put the Tor Expert Bundle in the 'tor' folder next to the app")
	}
	p.TorDir = filepath.Dir(p.TorExe)
	parent := filepath.Dir(p.TorDir)
	p.PTDir = firstDir(
		filepath.Join(p.TorDir, "pluggable_transports"),
		filepath.Join(parent, "pluggable_transports"),
	)
	p.GeoIP = firstExisting(
		filepath.Join(parent, "data", "geoip"),
		filepath.Join(p.TorDir, "data", "geoip"),
		filepath.Join(p.TorDir, "geoip"),
	)
	p.GeoIPv6 = firstExisting(
		filepath.Join(parent, "data", "geoip6"),
		filepath.Join(p.TorDir, "data", "geoip6"),
		filepath.Join(p.TorDir, "geoip6"),
	)
	return p, nil
}

// ptExec returns the path to use in a ClientTransportPlugin line. Tor splits
// the exec line on spaces, so we prefer a path relative to the tor directory
// (tor runs with that as its working directory).
func (p TorPaths) ptExec(name string) (string, bool) {
	if p.PTDir == "" {
		return "", false
	}
	full := firstExisting(filepath.Join(p.PTDir, name+exeSuffix()))
	if full == "" {
		return "", false
	}
	if rel, err := filepath.Rel(p.TorDir, full); err == nil && !strings.Contains(rel, " ") {
		return rel, true
	}
	if strings.Contains(full, " ") {
		return "\"" + full + "\"", true
	}
	return full, true
}

type TorStatus struct {
	State    string `json:"state"` // off, starting, on, stopping, error
	Progress int    `json:"progress"`
	Summary  string `json:"summary"`
	Error    string `json:"error,omitempty"`
}

type Tor struct {
	paths TorPaths
	log   *LogBuffer

	mu      sync.Mutex
	cmd     *exec.Cmd
	status  TorStatus
	done    chan struct{}
	onReady func()
	onExit  func(error)
}

func newTor(paths TorPaths, log *LogBuffer) *Tor {
	return &Tor{paths: paths, log: log, status: TorStatus{State: "off"}}
}

func (t *Tor) Status() TorStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status
}

func (t *Tor) setStatus(fn func(*TorStatus)) {
	t.mu.Lock()
	fn(&t.status)
	t.mu.Unlock()
}

func (t *Tor) dataDir() string { return filepath.Join(t.paths.DataRoot, "tor-data") }

var countryCodeRe = regexp.MustCompile(`^[a-z]{2}$`)

func (t *Tor) torrc(s Settings) (string, error) {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	w("DataDirectory %s", t.dataDir())
	w("ClientOnly 1")
	w("AvoidDiskWrites 1")
	w("Log notice stdout")
	w("SocksPort 127.0.0.1:%d IPv6Traffic", s.SocksPort)
	w("ControlPort 127.0.0.1:auto")
	w("ControlPortWriteToFile %s", filepath.Join(t.dataDir(), "control.port"))
	w("CookieAuthentication 1")
	w("CookieAuthFile %s", filepath.Join(t.dataDir(), "control_auth_cookie"))
	w("__OwningControllerProcess %d", os.Getpid())
	if s.Mode == ModeVPN {
		w("DNSPort 127.0.0.1:%d", s.DNSPort)
		w("AutomapHostsOnResolve 1")
		w("VirtualAddrNetworkIPv4 %s", torVirtualNet)
	}
	if t.paths.GeoIP != "" {
		w("GeoIPFile %s", t.paths.GeoIP)
	}
	if t.paths.GeoIPv6 != "" {
		w("GeoIPv6File %s", t.paths.GeoIPv6)
	}
	if cc := strings.ToLower(strings.TrimSpace(s.ExitCountry)); countryCodeRe.MatchString(cc) {
		w("ExitNodes {%s}", cc)
		w("StrictNodes 1")
	}

	bridges := bridgeLines(s, t.paths.loadPTConfig())
	if s.Bridge != BridgeNone {
		if len(bridges) == 0 {
			return "", errors.New("no bridges configured")
		}
		plugins, have := t.transportPlugins()
		for _, line := range plugins {
			w("%s", line)
		}
		for _, br := range bridges {
			tr := strings.Fields(br)[0]
			if strings.ContainsAny(tr, ".:[") { // plain "IP:port fingerprint" bridge
				continue
			}
			if !have[strings.ToLower(tr)] {
				return "", fmt.Errorf("pluggable transport %q not found in %s", tr, filepath.Join(t.paths.TorDir, "pluggable_transports"))
			}
		}
		w("UseBridges 1")
		for _, br := range bridges {
			w("Bridge %s", br)
		}
	}
	return b.String(), nil
}

var bootstrapRe = regexp.MustCompile(`Bootstrapped (\d+)%[^:]*: (.*)$`)

// Start launches tor. onReady fires once when bootstrap reaches 100%;
// onExit fires when the process ends for any reason.
func (t *Tor) Start(s Settings, onReady func(), onExit func(error)) error {
	t.mu.Lock()
	if t.cmd != nil {
		t.mu.Unlock()
		return errors.New("tor is already running")
	}
	t.mu.Unlock()

	if err := os.MkdirAll(t.dataDir(), 0o700); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(t.dataDir(), "control.port"))
	rc, err := t.torrc(s)
	if err != nil {
		return err
	}
	rcPath := filepath.Join(t.paths.DataRoot, "torrc")
	if err := os.WriteFile(rcPath, []byte(rc), 0o600); err != nil {
		return err
	}

	cmd := exec.Command(t.paths.TorExe, "-f", rcPath, "--defaults-torrc", filepath.Join(t.paths.DataRoot, "torrc-defaults-empty"))
	_ = os.WriteFile(filepath.Join(t.paths.DataRoot, "torrc-defaults-empty"), nil, 0o600)
	cmd.Dir = t.paths.TorDir
	hideWindow(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start tor: %w", err)
	}
	t.log.Add("tor started (pid %d), bridge=%s mode=%s", cmd.Process.Pid, s.Bridge, s.Mode)

	done := make(chan struct{})
	t.mu.Lock()
	t.cmd = cmd
	t.done = done
	t.onReady = onReady
	t.onExit = onExit
	t.status = TorStatus{State: "starting", Summary: "Starting"}
	t.mu.Unlock()

	go func() {
		ready := false
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			t.log.Add("[tor] %s", stripTorTimestamp(line))
			if m := bootstrapRe.FindStringSubmatch(line); m != nil {
				pct, _ := strconv.Atoi(m[1])
				t.setStatus(func(st *TorStatus) {
					st.Progress = pct
					st.Summary = m[2]
				})
				if pct == 100 && !ready {
					ready = true
					go func() {
						t.mu.Lock()
						cb := t.onReady
						t.mu.Unlock()
						if cb != nil {
							cb()
						}
					}()
				}
			}
		}
		werr := cmd.Wait()
		t.log.Add("tor exited: %v", werr)
		t.mu.Lock()
		cb := t.onExit
		stopping := t.status.State == "stopping"
		t.cmd = nil
		if stopping || werr == nil {
			t.status = TorStatus{State: "off"}
		} else {
			t.status = TorStatus{State: "error", Error: fmt.Sprintf("tor exited: %v", werr)}
		}
		t.mu.Unlock()
		close(done)
		if cb != nil {
			cb(werr)
		}
	}()
	return nil
}

func stripTorTimestamp(line string) string {
	// "Oct 23 00:41:02.123 [notice] ..." -> "[notice] ..."
	if i := strings.Index(line, " ["); i > 0 && i < 24 {
		return line[i+1:]
	}
	return line
}

// SetStatus marks tor as fully up once the app finished applying the mode.
func (t *Tor) MarkOn() {
	t.setStatus(func(st *TorStatus) { st.State = "on" })
}

func (t *Tor) Stop() {
	t.mu.Lock()
	cmd, done := t.cmd, t.done
	if cmd == nil {
		t.mu.Unlock()
		return
	}
	t.status.State = "stopping"
	t.mu.Unlock()

	// Ask politely over the control port, then kill.
	if err := t.control("SIGNAL SHUTDOWN"); err != nil {
		_ = cmd.Process.Kill()
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

// NewIdentity asks tor for fresh circuits.
func (t *Tor) NewIdentity() error { return t.control("SIGNAL NEWNYM") }

func (t *Tor) control(cmdline string) error {
	pb, err := os.ReadFile(filepath.Join(t.dataDir(), "control.port"))
	if err != nil {
		return err
	}
	addr := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(pb)), "PORT="))
	cookie, err := os.ReadFile(filepath.Join(t.dataDir(), "control_auth_cookie"))
	if err != nil {
		return err
	}
	c, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(c)
	send := func(s string) error {
		if _, err := fmt.Fprintf(c, "%s\r\n", s); err != nil {
			return err
		}
		line, err := r.ReadString('\n')
		if err != nil {
			return err
		}
		if !strings.HasPrefix(line, "250") {
			return fmt.Errorf("tor control: %s", strings.TrimSpace(line))
		}
		return nil
	}
	if err := send("AUTHENTICATE " + hex.EncodeToString(cookie)); err != nil {
		return err
	}
	return send(cmdline)
}
