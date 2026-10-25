//go:build linux

// Package mobile runs Albatross inside the Android app. It is built with
// gomobile bind into an Android library; the app shows the same web UI in
// a WebView and owns the VpnService.
//
// The engine works as on Windows in "proxy" mode: it runs the cores and a
// local proxy port. Where Windows would point the system proxy at that
// port, Android starts its VpnService instead, and the tunnel's packets are
// handed to the local proxy by tun2socks.
package mobile

import (
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/Albatrossvpn/vp/internal/engine"
	"github.com/Albatrossvpn/vp/internal/geo"
	"github.com/Albatrossvpn/vp/internal/roots"
	"github.com/Albatrossvpn/vp/internal/sources"
	"github.com/Albatrossvpn/vp/internal/ui"
)

// Platform is implemented by the Android app.
type Platform interface {
	// StartVPN brings the VpnService up; it calls StartTun once the tunnel
	// exists. It may be called again while the VPN is up.
	StartVPN()
	// StopVPN takes the VpnService down.
	StopVPN()
	// OpenURL shows a link in the browser.
	OpenURL(url string)
	// OpenVPNSettings shows the system VPN settings, where the user can
	// turn on Always-on VPN and "Block connections without VPN".
	OpenVPNSettings()
	// Quit closes the app.
	Quit()
	// ShareText opens the system share sheet with text.
	ShareText(text string)
	// ShareApp opens the system share sheet with the app's installer.
	ShareApp()
	// LanAddrs lists the device's IPv4 addresses on its hotspot and
	// Wi-Fi, comma-separated. Go cannot list them on Android 11 and later.
	LanAddrs() string
	// InstallUpdate opens the system installer on a downloaded APK of a
	// newer version, which installs over this one (same signing key).
	InstallUpdate(path string) error
	// LocationSetup says what the phone still needs before the app may set
	// its location: "" when it is ready, "developer" while Developer
	// options are off, "mock_app" until Albatross is the selected mock
	// location app.
	LocationSetup() string
	// OpenLocationSettings shows the system screen for one of those steps:
	// About phone for "developer", Developer options for "mock_app".
	OpenLocationSettings(step string)
}

var (
	mu    sync.Mutex
	eng   *engine.Engine
	srv   *ui.Server
	uiURL string
	vpn   = &vpnProxy{}
)

// Version is set at build time with -ldflags "-X .../mobile.Version=...".
var Version = "dev"

// Start runs the engine and the UI server once and returns the UI's
// address (with its access token). dataDir is the app's private storage,
// libDir the directory holding the bundled cores (libsingbox.so ...), and
// dns the network's resolver for direct lookups.
func Start(dataDir, libDir, dns string, p Platform) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	if eng != nil {
		return uiURL, nil
	}
	// A phone has far less memory to spare than a PC: collect sooner.
	debug.SetGCPercent(50)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", err
	}
	// Before anything verifies a certificate, since Go reads the roots
	// once. If the file cannot be written, the phone's own roots remain.
	roots.Use(dataDir)
	vpn.platform = p
	// ALBATROSS_SOURCES replaces the built-in sources, as on the desktop.
	var only []sources.Source
	for _, s := range strings.Split(os.Getenv("ALBATROSS_SOURCES"), ",") {
		if src, ok := sources.User(s); ok {
			src.Builtin = true
			only = append(only, src)
		}
	}
	e, err := engine.New(engine.Options{
		Sources:       only,
		DataDir:       filepath.Join(dataDir, "albatross"),
		AppDirs:       []string{libDir},
		SysProxy:      vpn,
		Version:       Version,
		LocalDNS:      dns,
		Mobile:        true,
		InstallUpdate: p.InstallUpdate,
		LANAddrs: func() []string {
			var out []string
			for _, a := range strings.Split(p.LanAddrs(), ",") {
				if a = strings.TrimSpace(a); a != "" {
					out = append(out, a)
				}
			}
			return out
		},
	})
	if err != nil {
		return "", err
	}
	e.Start()
	s, u, err := ui.Start(e, ui.Hooks{
		Quit:                 func() { go p.Quit() },
		OpenURL:              func(link string) error { p.OpenURL(link); return nil },
		ShareText:            func(text string) error { p.ShareText(text); return nil },
		ShareApp:             func() error { p.ShareApp(); return nil },
		OpenVPNSettings:      func() error { p.OpenVPNSettings(); return nil },
		LocationSetup:        p.LocationSetup,
		OpenLocationSettings: func(step string) error { p.OpenLocationSettings(step); return nil },
	}, 0)
	if err != nil {
		e.Close()
		return "", err
	}
	eng, srv, uiURL = e, s, u
	return uiURL, nil
}

// Stop disconnects and shuts everything down.
func Stop() {
	mu.Lock()
	e, s := eng, srv
	eng, srv, uiURL = nil, nil, ""
	mu.Unlock()
	if s != nil {
		s.Close()
	}
	if e != nil {
		e.Close()
	}
	StopTun()
}

// Disconnect stops the VPN, as the disconnect button does. The app calls it
// when Android revokes the VPN (another VPN started, or the user turned it
// off in the system settings).
func Disconnect() {
	mu.Lock()
	e := eng
	mu.Unlock()
	if e != nil {
		e.Disconnect()
	}
}

// Resume connects again if the user left the VPN on; the app calls it when
// Android starts the VPN by itself (Always-on VPN). It reports whether a
// connection is on its way.
func Resume() bool {
	mu.Lock()
	e := eng
	mu.Unlock()
	return e != nil && e.Resume()
}

// Connected reports whether traffic is flowing through a server, for the
// VPN notification: while it searches or reconnects, it is not.
func Connected() bool {
	mu.Lock()
	e := eng
	mu.Unlock()
	return e != nil && e.Snapshot().Status == engine.StatusConnected
}

// MockLocation is where the phone should say it is, for the VPN service's
// ticker. It is "" while the engine is not running or Override GPS location
// is off (the app removes its mock location), and "keep" while no country
// with a known place is connected yet (the app keeps the location it set).
// Otherwise it is "lat,lon": a point in the exit country.
func MockLocation() string {
	mu.Lock()
	e := eng
	mu.Unlock()
	if e == nil || !e.Settings().OverrideGPS {
		return ""
	}
	c := e.Snapshot().Conn
	if c == nil {
		return "keep"
	}
	lat, lon, ok := geo.Place(c.Country)
	if !ok {
		return "keep"
	}
	return mockPoint(lat, lon, c.ExitIP)
}

// mockPoint moves a place by up to about 0.02 degrees each way, by an amount
// that comes from the exit address: the point changes with the server, not
// every time it is asked for.
func mockPoint(lat, lon float64, exitIP string) string {
	h := fnv.New32a()
	h.Write([]byte(exitIP))
	v := h.Sum32()
	lat += (float64(v&0xffff)/0xffff - 0.5) * 0.04
	lon += (float64(v>>16)/0xffff - 0.5) * 0.04
	return fmt.Sprintf("%.6f,%.6f", lat, lon)
}

// SetDNS tells the engine the current network's resolver; the app calls it
// when the device moves between networks.
func SetDNS(addr string) {
	mu.Lock()
	e := eng
	mu.Unlock()
	if e != nil && addr != "" {
		e.SetLocalDNS(addr)
	}
}

// vpnProxy is the engine's "system proxy" on Android: setting it means
// routing the whole device through the local proxy with the VPN.
type vpnProxy struct {
	mu       sync.Mutex
	platform Platform
	addr     string
	on       bool
}

func (v *vpnProxy) Set(addr string, _ []string) error {
	v.mu.Lock()
	if v.platform == nil {
		v.mu.Unlock()
		return errors.New("no platform")
	}
	moved := v.on && v.addr != addr
	v.addr, v.on = addr, true
	p := v.platform
	v.mu.Unlock()
	if moved {
		// The proxy port changed (another app took it); point the running
		// tunnel at the new one.
		retarget(addr)
	}
	p.StartVPN()
	return nil
}

func (v *vpnProxy) Clear() error {
	v.mu.Lock()
	on, p := v.on, v.platform
	// Forgetting the address makes StartTun fail, so a tunnel Android
	// brings back by itself (Always-on VPN) cannot reach a dead proxy.
	v.on, v.addr = false, ""
	v.mu.Unlock()
	StopTun()
	if on && p != nil {
		p.StopVPN()
	}
	return nil
}

func (v *vpnProxy) Owned(string) bool { return false }

func (v *vpnProxy) target() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.addr
}

// LanNets lists the local network's address ranges, comma separated, for
// the app to leave outside the tunnel.
func LanNets() string { return strings.Join(engine.LANNets, ",") }
