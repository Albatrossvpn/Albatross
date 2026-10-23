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
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/cezomagoanlace29861-jpg/vp/internal/engine"
	"github.com/cezomagoanlace29861-jpg/vp/internal/sources"
	"github.com/cezomagoanlace29861-jpg/vp/internal/ui"
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
		Sources:  only,
		DataDir:  filepath.Join(dataDir, "albatross"),
		AppDirs:  []string{libDir},
		SysProxy: vpn,
		Version:  Version,
		LocalDNS: dns,
		Mobile:   true,
	})
	if err != nil {
		return "", err
	}
	e.Start()
	s, u, err := ui.Start(e, ui.Hooks{
		Quit:            func() { go p.Quit() },
		OpenURL:         func(link string) error { p.OpenURL(link); return nil },
		ShareText:       func(text string) error { p.ShareText(text); return nil },
		ShareApp:        func() error { p.ShareApp(); return nil },
		OpenVPNSettings: func() error { p.OpenVPNSettings(); return nil },
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
