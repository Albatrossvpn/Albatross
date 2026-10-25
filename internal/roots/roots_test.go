package roots

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The bundle parses and holds the roots old phones miss.
func TestBundle(t *testing.T) {
	names := map[string]bool{}
	n := 0
	for rest := bundle; ; {
		var b *pem.Block
		if b, rest = pem.Decode(rest); b == nil {
			break
		}
		c, err := x509.ParseCertificate(b.Bytes)
		if err != nil {
			t.Fatalf("certificate %d: %v", n+1, err)
		}
		names[c.Subject.CommonName] = true
		n++
	}
	if n < 100 {
		t.Fatalf("only %d certificates in the bundle", n)
	}
	for _, want := range []string{"ISRG Root X1", "SSL.com Root Certification Authority ECC"} {
		if !names[want] {
			t.Errorf("the bundle lacks %s", want)
		}
	}
}

// Off Android nothing is written and the environment stays as it was.
func TestUseOffAndroid(t *testing.T) {
	if runtime.GOOS == "android" {
		t.Skip("on Android")
	}
	t.Setenv("SSL_CERT_FILE", "")
	dir := t.TempDir()
	if err := Use(dir); err != nil {
		t.Fatal(err)
	}
	if v := os.Getenv("SSL_CERT_FILE"); v != "" {
		t.Fatalf("SSL_CERT_FILE = %q", v)
	}
	if _, err := os.Stat(filepath.Join(dir, fileName)); !os.IsNotExist(err) {
		t.Fatalf("a bundle was written: %v", err)
	}
}

// A stale file is replaced, and the variable names it.
func TestUseWrites(t *testing.T) {
	t.Setenv("SSL_CERT_FILE", "")
	dir := t.TempDir()
	path := filepath.Join(dir, fileName)
	os.WriteFile(path, []byte("stale"), 0o644)
	if err := use(dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, bundle) {
		t.Fatal("the stale file was kept")
	}
	if v := os.Getenv("SSL_CERT_FILE"); v != path {
		t.Fatalf("SSL_CERT_FILE = %q, want %q", v, path)
	}
}

// A server whose certificate chains to a root only the bundle holds
// verifies after use, and not without it, while one that chains to the
// phone's own store (SSL_CERT_DIR here) verifies either way. Each half runs
// in a child process, since Go reads the system roots only once.
func TestUseTrustsBundle(t *testing.T) {
	if mode := os.Getenv("ALBATROSS_ROOTS_CHILD"); mode != "" {
		if mode == "use" {
			ca, err := os.ReadFile(os.Getenv("ALBATROSS_ROOTS_CA"))
			if err != nil {
				t.Fatal(err)
			}
			bundle = ca
			if err := use(t.TempDir()); err != nil {
				t.Fatal(err)
			}
		}
		c := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil}}
		for _, u := range strings.Fields(os.Getenv("ALBATROSS_ROOTS_URLS")) {
			resp, err := c.Get(u)
			if err != nil {
				fmt.Println("get:", err)
				continue
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			fmt.Println("get:", string(body))
		}
		return
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "android" {
		t.Skip("SSL_CERT_FILE and SSL_CERT_DIR are read only on Linux and Android")
	}
	dir := t.TempDir()
	server := func(name string) (*httptest.Server, []byte) {
		caPEM, cert := testCA(t, name)
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, name+" verified") }))
		srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
		// The handshake the child refuses is expected.
		srv.Config.ErrorLog = log.New(io.Discard, "", 0)
		srv.StartTLS()
		t.Cleanup(srv.Close)
		return srv, caPEM
	}
	bundled, bundledCA := server("bundle")
	phone, phoneCA := server("phone")
	caFile := filepath.Join(dir, "ca.pem")
	os.WriteFile(caFile, bundledCA, 0o644)
	store := filepath.Join(dir, "cacerts")
	os.Mkdir(store, 0o755)
	os.WriteFile(filepath.Join(store, "phone.0"), phoneCA, 0o644)
	run := func(mode string) string {
		cmd := exec.Command(os.Args[0], "-test.run=^TestUseTrustsBundle$", "-test.v")
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "SSL_CERT_FILE=") && !strings.HasPrefix(kv, "SSL_CERT_DIR=") {
				cmd.Env = append(cmd.Env, kv)
			}
		}
		cmd.Env = append(cmd.Env, "ALBATROSS_ROOTS_CHILD="+mode, "ALBATROSS_ROOTS_CA="+caFile,
			"ALBATROSS_ROOTS_URLS="+bundled.URL+" "+phone.URL, "SSL_CERT_DIR="+store)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s child: %v\n%s", mode, err, out)
		}
		return string(out)
	}
	out := run("plain")
	if !strings.Contains(out, "certificate signed by unknown authority") || !strings.Contains(out, "get: phone verified") {
		t.Fatalf("without the bundle, want only the phone's root trusted:\n%s", out)
	}
	out = run("use")
	if !strings.Contains(out, "get: bundle verified") || !strings.Contains(out, "get: phone verified") {
		t.Fatalf("with the bundle, want both roots trusted:\n%s", out)
	}
}

// testCA makes a root and a certificate it signs for 127.0.0.1, and
// returns the root as PEM.
func testCA(t *testing.T, name string) ([]byte, tls.Certificate) {
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Albatross test root " + name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ = x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	return caPEM, tls.Certificate{Certificate: [][]byte{leafDER}, PrivateKey: key}
}
