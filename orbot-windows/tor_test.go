package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBundle lays out files like the Tor Expert Bundle.
func fakeBundle(t *testing.T, ptConfig string) TorPaths {
	t.Helper()
	dir := t.TempDir()
	pt := filepath.Join(dir, "tor", "pluggable_transports")
	if err := os.MkdirAll(pt, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{
		filepath.Join(dir, "tor", torExeName()),
		filepath.Join(pt, "lyrebird"+exeSuffix()),
		filepath.Join(pt, "conjure-client"+exeSuffix()),
	} {
		if err := os.WriteFile(f, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if ptConfig != "" {
		if err := os.WriteFile(filepath.Join(pt, "pt_config.json"), []byte(ptConfig), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := findTor(dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

const samplePTConfig = `{
  "recommendedDefault": "obfs4",
  "pluggableTransports": {
    "lyrebird" : "ClientTransportPlugin meek_lite,obfs2,obfs3,obfs4,scramblesuit,webtunnel exec ${pt_path}lyrebird.exe",
    "snowflake": "ClientTransportPlugin snowflake exec ${pt_path}lyrebird.exe",
    "conjure" : "ClientTransportPlugin conjure exec ${pt_path}conjure-client${pt_ext} -registerURL https://registration.refraction.network/api"
  },
  "bridges": {
    "snowflake": ["snowflake 192.0.2.9:80 AAAA fingerprint=AAAA url=https://example.invalid/"]
  }
}`

func TestTorrcUsesPTConfig(t *testing.T) {
	tor := newTor(fakeBundle(t, samplePTConfig), newLogBuffer(10))
	s := defaultSettings()
	s.Bridge = BridgeSnowflake
	rc, err := tor.torrc(s)
	if err != nil {
		t.Fatal(err)
	}
	lyre := filepath.Join("pluggable_transports", "lyrebird"+exeSuffix())
	for _, want := range []string{
		"ClientTransportPlugin meek_lite,obfs2,obfs3,obfs4,scramblesuit,webtunnel exec " + lyre + "\n",
		"ClientTransportPlugin snowflake exec " + lyre + "\n",
		"-registerURL https://registration.refraction.network/api\n",
		"Bridge snowflake 192.0.2.9:80 AAAA",
		"UseBridges 1\n",
	} {
		if !strings.Contains(rc, want) {
			t.Errorf("torrc missing %q:\n%s", want, rc)
		}
	}
}

func TestTorrcWithoutPTConfigRejectsMissingSnowflake(t *testing.T) {
	tor := newTor(fakeBundle(t, ""), newLogBuffer(10))
	s := defaultSettings()
	s.Bridge = BridgeSnowflake
	if _, err := tor.torrc(s); err == nil {
		t.Fatal("expected an error when no transport provides snowflake")
	}
	s.Bridge = BridgeObfs4
	rc, err := tor.torrc(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rc, "Bridge obfs4 ") {
		t.Errorf("expected Orbot's obfs4 bridges:\n%s", rc)
	}
}

func TestParseCustomBridges(t *testing.T) {
	got := parseCustomBridges("Bridge obfs4 1.2.3.4:443 FP cert=x iat-mode=0\n\n# c\nwebtunnel [2001:db8::1]:443 FP url=https://e/x\n")
	if len(got) != 2 || got[0] != "obfs4 1.2.3.4:443 FP cert=x iat-mode=0" || !strings.HasPrefix(got[1], "webtunnel ") {
		t.Fatalf("got %q", got)
	}
}
