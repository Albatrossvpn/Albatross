//go:build windows

package main

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

const inetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// ProxyBackup is the user's WinINet proxy configuration before Orbot changed it.
type ProxyBackup struct {
	Enable   uint32 `json:"enable"`
	Server   string `json:"server"`
	Override string `json:"override"`
	HadAuto  bool   `json:"hadAuto"`
	AutoURL  string `json:"autoUrl"`
}

func readProxy() (*ProxyBackup, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, inetSettingsKey, registry.QUERY_VALUE)
	if err != nil {
		return nil, err
	}
	defer k.Close()
	b := &ProxyBackup{}
	if v, _, err := k.GetIntegerValue("ProxyEnable"); err == nil {
		b.Enable = uint32(v)
	}
	b.Server, _, _ = k.GetStringValue("ProxyServer")
	b.Override, _, _ = k.GetStringValue("ProxyOverride")
	if v, _, err := k.GetStringValue("AutoConfigURL"); err == nil {
		b.HadAuto, b.AutoURL = true, v
	}
	return b, nil
}

// setSystemProxy points WinINet (Edge, Chrome, most Windows apps) at our
// HTTP proxy and returns the previous settings.
func setSystemProxy(httpAddr string) (*ProxyBackup, error) {
	prev, err := readProxy()
	if err != nil {
		return nil, err
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, inetSettingsKey, registry.SET_VALUE)
	if err != nil {
		return nil, err
	}
	defer k.Close()
	if err := k.SetStringValue("ProxyServer", httpAddr); err != nil {
		return nil, err
	}
	_ = k.SetStringValue("ProxyOverride", "localhost;127.*;10.*;172.16.*;192.168.*;<local>")
	_ = k.DeleteValue("AutoConfigURL")
	if err := k.SetDWordValue("ProxyEnable", 1); err != nil {
		return nil, err
	}
	refreshInternetSettings()
	return prev, nil
}

func restoreSystemProxy(b *ProxyBackup) error {
	if b == nil {
		return nil
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, inetSettingsKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	_ = k.SetDWordValue("ProxyEnable", b.Enable)
	if b.Server == "" {
		_ = k.DeleteValue("ProxyServer")
	} else {
		_ = k.SetStringValue("ProxyServer", b.Server)
	}
	if b.Override == "" {
		_ = k.DeleteValue("ProxyOverride")
	} else {
		_ = k.SetStringValue("ProxyOverride", b.Override)
	}
	if b.HadAuto {
		_ = k.SetStringValue("AutoConfigURL", b.AutoURL)
	}
	refreshInternetSettings()
	return nil
}

var (
	wininet                = syscall.NewLazyDLL("wininet.dll")
	procInternetSetOptionW = wininet.NewProc("InternetSetOptionW")
)

func refreshInternetSettings() {
	const (
		optionSettingsChanged = 39
		optionRefresh         = 37
	)
	if procInternetSetOptionW.Find() != nil {
		return
	}
	procInternetSetOptionW.Call(0, optionSettingsChanged, uintptr(unsafe.Pointer(nil)), 0)
	procInternetSetOptionW.Call(0, optionRefresh, uintptr(unsafe.Pointer(nil)), 0)
}
