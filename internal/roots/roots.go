// Package roots makes old phones trust today's certificate authorities.
// Android 6 to 7.0 lack ISRG Root X1 (Let's Encrypt), and Android up to
// 8.1 lack SSL.com's ECC root (1.1.1.1's DNS over HTTPS), so the update
// downloads, WARP registration and servers with those certificates fail
// there. Go reads the system roots from SSL_CERT_FILE as well as from the
// phone's own store, and the cores are Go programs that inherit the
// engine's environment, so one file fixes them all.
package roots

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"
	"runtime"
)

// bundle is Mozilla's set of roots, which build/android/build.sh refreshes
// from the build machine before building the app.
//
//go:embed roots.pem
var bundle []byte

// fileName is the bundle's name in the app's data directory.
const fileName = "roots.pem"

// Use writes the bundled roots to dir and points SSL_CERT_FILE at them, so
// this process and every core it starts afterwards trust them besides the
// phone's own. Go reads the system roots once, at the first certificate it
// verifies, so this must run before any TLS connection. It does nothing
// off Android: Windows verifies with its own store.
func Use(dir string) error {
	if runtime.GOOS != "android" {
		return nil
	}
	return use(dir)
}

func use(dir string) error {
	path := filepath.Join(dir, fileName)
	if old, err := os.ReadFile(path); err != nil || !bytes.Equal(old, bundle) {
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, bundle, 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, path); err != nil {
			return err
		}
	}
	return os.Setenv("SSL_CERT_FILE", path)
}
