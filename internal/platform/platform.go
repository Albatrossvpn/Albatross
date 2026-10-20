// Package platform wraps the operating-system features the app needs:
// the system proxy, administrator rights, start at login, a single running
// instance and where to keep data.
package platform

import (
	"os"
	"path/filepath"
)

// AppName is used for folders, registry keys and the window title.
const AppName = "Albatross"

// DataDir is where settings and caches live.
func DataDir() string {
	if d := os.Getenv("ALBATROSS_DATA"); d != "" {
		return d
	}
	return dataDir()
}

// ExeDir is the folder of the running executable.
func ExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return filepath.Dir(exe)
}

// ExeName is the running executable's file name.
func ExeName() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Base(exe)
}
