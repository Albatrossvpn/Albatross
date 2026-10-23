package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTorConfig(t *testing.T) {
	dir := t.TempDir()
	torBin := filepath.Join(dir, "tor")
	pt := filepath.Join(dir, torPTDir)
	os.MkdirAll(pt, 0o755)
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	os.WriteFile(filepath.Join(pt, "lyrebird"+exe), []byte("x"), 0o755)
	os.WriteFile(filepath.Join(pt, torPTFile), []byte(`{
  "pluggableTransports": {
    "lyrebird": "ClientTransportPlugin meek_lite,obfs4,webtunnel exec ${pt_path}lyrebird",
    "snowflake": "ClientTransportPlugin snowflake exec ${pt_path}snowflake-client"
  },
  "bridges": {
    "obfs4": ["obfs4 192.0.2.1:443 AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA cert=x iat-mode=0"],
    "snowflake": ["snowflake 192.0.2.3:80 BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB url=https://x"],
    "meek": ["meek_lite 192.0.2.18:80 CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC url=https://y front=z"]
  }
}`), 0o644)
	rc, err := TorConfig(torBin, 9050, filepath.Join(dir, "data dir"), []byte(`{"webtunnel":["webtunnel [2001:db8::1]:443 DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD url=https://w"],"obfs4":["obfs4 192.0.2.1:443 AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA cert=x iat-mode=0"]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"SocksPort 127.0.0.1:9050",
		"ClientTransportPlugin meek_lite,obfs4,webtunnel exec " + filepath.Join(torPTDir, "lyrebird"+exe),
		"Bridge webtunnel [2001:db8::1]:443",
		"Bridge obfs4 192.0.2.1:443",
		"Bridge meek_lite 192.0.2.18:80",
		`DataDirectory "`,
	} {
		if !strings.Contains(rc, want) {
			t.Errorf("torrc lacks %q:\n%s", want, rc)
		}
	}
	// snowflake-client is not installed, so its bridges are left out, and
	// the obfs4 bridge both lists carry appears once.
	if strings.Contains(rc, "snowflake") || strings.Count(rc, "Bridge obfs4") != 1 {
		t.Errorf("wrong bridges:\n%s", rc)
	}
	// webtunnel goes before obfs4.
	if strings.Index(rc, "Bridge webtunnel") > strings.Index(rc, "Bridge obfs4") {
		t.Errorf("wrong order:\n%s", rc)
	}
	// A real tor, where installed, must accept the file.
	if real, err := exec.LookPath("tor"); err == nil {
		path := filepath.Join(dir, "torrc")
		os.WriteFile(path, []byte(strings.ReplaceAll(rc, "__OwningControllerProcess", "#")), 0o600)
		cmd := exec.Command(real, "--verify-config", "-f", path)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("tor rejects the config: %v\n%s", err, out)
		}
	}
}
