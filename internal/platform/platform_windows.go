//go:build windows

package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func dataDir() string {
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		return filepath.Join(d, AppName)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "AppData", "Local", AppName)
}

// IsAdmin reports whether the process runs elevated.
func IsAdmin() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// RelaunchAsAdmin starts this executable elevated (Windows shows the UAC
// prompt). The caller should exit afterwards.
func RelaunchAsAdmin(args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString(strings.Join(append(args, "--elevated"), " "))
	dir, _ := windows.UTF16PtrFromString(filepath.Dir(exe))
	return windows.ShellExecute(0, verb, file, params, dir, windows.SW_SHOWNORMAL)
}

const internetSettings = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

var (
	wininet           = windows.NewLazySystemDLL("wininet.dll")
	internetSetOption = wininet.NewProc("InternetSetOptionW")
)

// notifyProxyChange tells running programs to reload proxy settings.
func notifyProxyChange() {
	const settingsChanged, refresh = 39, 37
	internetSetOption.Call(0, settingsChanged, 0, 0)
	internetSetOption.Call(0, refresh, 0, 0)
}

// SystemProxy points Windows' proxy settings at the app and restores the
// previous settings afterwards.
type SystemProxy struct {
	mu    sync.Mutex
	saved *savedProxy
}

type savedProxy struct {
	enable   uint64
	server   string
	override string
	pac      string
	hadPAC   bool
}

// Set enables the system proxy.
func (p *SystemProxy) Set(addr string, bypass []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettings, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if p.saved == nil {
		s := &savedProxy{}
		s.enable, _, _ = k.GetIntegerValue("ProxyEnable")
		s.server, _, _ = k.GetStringValue("ProxyServer")
		s.override, _, _ = k.GetStringValue("ProxyOverride")
		s.pac, _, err = k.GetStringValue("AutoConfigURL")
		s.hadPAC = err == nil
		if !strings.Contains(s.server, "127.0.0.1:") {
			p.saved = s
		} else {
			p.saved = &savedProxy{}
		}
	}
	if err := k.SetDWordValue("ProxyEnable", 1); err != nil {
		return err
	}
	if err := k.SetStringValue("ProxyServer", addr); err != nil {
		return err
	}
	if err := k.SetStringValue("ProxyOverride", strings.Join(bypass, ";")); err != nil {
		return err
	}
	// A PAC script would take precedence over the proxy.
	k.DeleteValue("AutoConfigURL")
	notifyProxyChange()
	return nil
}

// Clear restores the settings from before Set.
func (p *SystemProxy) Clear() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettings, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	s := p.saved
	p.saved = nil
	if s == nil || s.server == "" {
		k.SetDWordValue("ProxyEnable", 0)
	} else {
		k.SetDWordValue("ProxyEnable", uint32(s.enable))
		k.SetStringValue("ProxyServer", s.server)
		k.SetStringValue("ProxyOverride", s.override)
	}
	if s != nil && s.hadPAC {
		k.SetStringValue("AutoConfigURL", s.pac)
	}
	notifyProxyChange()
	return nil
}

// Owned reports whether Windows still points at addr from a run that
// crashed.
func (p *SystemProxy) Owned(addr string) bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettings, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	en, _, _ := k.GetIntegerValue("ProxyEnable")
	srv, _, _ := k.GetStringValue("ProxyServer")
	return en == 1 && srv == addr
}

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// SetAutostart adds or removes the app from programs started at login.
func SetAutostart(enable bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !enable {
		err := k.DeleteValue(AppName)
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue(AppName, fmt.Sprintf(`"%s" --autostart`, exe))
}

var instanceMutex windows.Handle

// SingleInstance claims the app-wide mutex. It returns false when another
// copy is already running (after asking it to show its window). wait lets
// an elevated relaunch wait for the old copy to exit.
func SingleInstance(title string, wait time.Duration) bool {
	name, _ := windows.UTF16PtrFromString("Local\\" + AppName + "-single-instance")
	deadline := time.Now().Add(wait)
	for {
		h, err := windows.CreateMutex(nil, false, name)
		if err == nil {
			instanceMutex = h
			return true
		}
		if h != 0 {
			windows.CloseHandle(h)
		}
		if err != windows.ERROR_ALREADY_EXISTS || time.Now().After(deadline) {
			showExisting(title)
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// ReleaseInstance frees the mutex before an elevated relaunch.
func ReleaseInstance() {
	if instanceMutex != 0 {
		windows.CloseHandle(instanceMutex)
		instanceMutex = 0
	}
}

var (
	user32              = windows.NewLazySystemDLL("user32.dll")
	findWindow          = user32.NewProc("FindWindowW")
	showWindow          = user32.NewProc("ShowWindow")
	setForegroundWindow = user32.NewProc("SetForegroundWindow")
	messageBox          = user32.NewProc("MessageBoxW")
)

func showExisting(title string) {
	t, _ := windows.UTF16PtrFromString(title)
	h, _, _ := findWindow.Call(0, uintptr(unsafe.Pointer(t)))
	if h != 0 {
		const swRestore = 9
		showWindow.Call(h, swRestore)
		setForegroundWindow.Call(h)
	}
}

// Alert shows a blocking message box.
func Alert(title, text string) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(text)
	const mbIconWarning, mbRTL = 0x30, 0x00100000 | 0x00080000
	messageBox.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), mbIconWarning|mbRTL)
}

// OpenURL opens a link in the default browser.
func OpenURL(u string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	file, _ := windows.UTF16PtrFromString(u)
	return windows.ShellExecute(0, verb, file, nil, nil, windows.SW_SHOWNORMAL)
}
