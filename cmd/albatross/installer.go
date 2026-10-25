package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Albatrossvpn/vp/internal/engine"
	"github.com/Albatrossvpn/vp/internal/platform"
)

// saveInstaller downloads the newest installer the app knows of (through
// the VPN when it is up) into the user's Downloads folder and shows it
// there, so the app can be passed on by USB or any messenger when its
// website cannot be reached.
func saveInstaller(eng *engine.Engine) (string, error) {
	src, err := eng.InstallerFile()
	if err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, "Downloads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// Albatross-<version>.exe becomes Albatross-Setup-<version>.exe, the
	// release's own name.
	name := strings.Replace(filepath.Base(src), "Albatross-", "Albatross-Setup-", 1)
	dst := filepath.Join(dir, name)
	if err := copyFile(src, dst); err != nil {
		return "", err
	}
	platform.RevealFile(dst)
	return dst, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err == nil {
		err = out.Close()
	} else {
		out.Close()
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
