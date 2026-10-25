//go:build !windows

package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

func dataDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "albatross")
	}
	return filepath.Join(os.TempDir(), "albatross")
}

// IsAdmin reports whether the process runs as root.
func IsAdmin() bool { return os.Geteuid() == 0 }

// RelaunchAsAdmin is only supported on Windows.
func RelaunchAsAdmin(...string) error { return os.ErrPermission }

// SystemProxy is a no-op outside Windows; use the proxy port directly.
type SystemProxy struct{}

func (*SystemProxy) Set(string, []string) error { return nil }
func (*SystemProxy) Clear() error               { return nil }
func (*SystemProxy) Owned(string) bool          { return false }

// SetAutostart is only supported on Windows.
func SetAutostart(bool) error { return nil }

// SingleInstance always succeeds outside Windows.
func SingleInstance(string, time.Duration) bool { return true }

// ReleaseInstance does nothing outside Windows.
func ReleaseInstance() {}

// Alert prints to stderr outside Windows.
func Alert(title, text string) { os.Stderr.WriteString(title + ": " + text + "\n") }

// OpenURL opens a link in the default browser.
// RevealFile opens the folder that holds path.
func RevealFile(path string) error {
	return OpenURL(filepath.Dir(path))
}

func OpenURL(u string) error {
	cmd := "xdg-open"
	if runtime.GOOS == "darwin" {
		cmd = "open"
	}
	return exec.Command(cmd, u).Start()
}

// SystemDNS is the network's resolver; unknown here (the Android app
// passes it from the system, and Linux cores read resolv.conf themselves).
func SystemDNS() string { return "" }
