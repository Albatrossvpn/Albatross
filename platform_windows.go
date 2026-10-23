//go:build windows

package main

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}

func torExeName() string { return "tor.exe" }
func exeSuffix() string  { return ".exe" }

func isElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// relaunchElevated restarts the app with a UAC prompt; the caller exits.
func relaunchElevated() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	args, _ := windows.UTF16PtrFromString("--connect")
	cwd, _ := windows.UTF16PtrFromString(appDir())
	return windows.ShellExecute(0, verb, file, args, cwd, windows.SW_SHOWNORMAL)
}

var instanceMutex windows.Handle

// acquireSingleInstance waits briefly for a previous instance (for example
// the non-elevated one that is relaunching us) to exit.
func acquireSingleInstance() bool {
	name, _ := windows.UTF16PtrFromString(`Local\OrbotWindowsSingleInstance`)
	deadline := time.Now().Add(10 * time.Second)
	for {
		h, err := windows.CreateMutex(nil, true, name)
		if err == nil {
			instanceMutex = h
			return true
		}
		if h != 0 {
			windows.CloseHandle(h)
		}
		if err != windows.ERROR_ALREADY_EXISTS || time.Now().After(deadline) {
			return false
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// runWindow shows the UI in a WebView2 window (built into Windows 10/11).
// If WebView2 is missing it falls back to the default browser.
func runWindow(url string, onClose func()) {
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		DataPath:  dataDir() + `\webview`,
		WindowOptions: webview2.WindowOptions{
			Title:  "Orbot",
			Width:  440,
			Height: 760,
			IconId: 1, // first icon resource embedded by go-winres
			Center: true,
		},
	})
	if w == nil {
		openBrowser(url)
		select {}
	}
	defer w.Destroy()
	w.Navigate(url)
	w.Run()
	onClose()
}

func openBrowser(url string) {
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		return
	}
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}
