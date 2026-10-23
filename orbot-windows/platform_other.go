//go:build !windows

package main

import (
	"errors"
	"os/exec"
)

// Non-Windows stubs so the core (tor, proxy, UI) can be built and tested
// on other systems. The release target is windows/amd64.

type ProxyBackup struct{}

func setSystemProxy(string) (*ProxyBackup, error) {
	return nil, errors.New("system proxy is only supported on Windows")
}
func restoreSystemProxy(*ProxyBackup) error { return nil }

func hideWindow(*exec.Cmd) {}
func torExeName() string   { return "tor" }
func exeSuffix() string    { return "" }
func isElevated() bool     { return true }
func relaunchElevated() error {
	return errors.New("not supported")
}
func acquireSingleInstance() bool { return true }

type VPN struct{}

func startVPN(cfg VPNConfig, log *LogBuffer) (*VPN, error) {
	return nil, errors.New("VPN mode is only supported on Windows")
}
func (v *VPN) Close() {}

func runWindow(url string, onClose func()) {
	println("UI:", url)
	openBrowser(url)
	select {}
}

func openBrowser(url string) {
	_ = exec.Command("xdg-open", url).Start()
}
