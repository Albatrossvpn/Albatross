package tester

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// connectProxy forwards CONNECT requests to where they ask, as a server
// would.
func connectProxy(t *testing.T) *url.URL {
	t.Helper()
	p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up, err := net.Dial("tcp", r.Host)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			up.Close()
			return
		}
		go func() { io.Copy(up, conn); up.Close() }()
		io.Copy(conn, up)
		conn.Close()
	}))
	t.Cleanup(p.Close)
	u, _ := url.Parse(p.URL)
	return u
}

func TestForgedCertificates(t *testing.T) {
	trace := func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ip=203.0.113.9\nloc=DE\n") }
	real := httptest.NewTLSServer(http.HandlerFunc(trace))
	defer real.Close()
	// A server reading traffic shows its own certificate, which the
	// device does not trust.
	fake := httptest.NewUnstartedServer(http.HandlerFunc(trace))
	fake.TLS = &tls.Config{Certificates: []tls.Certificate{selfSigned(t)}}
	fake.StartTLS()
	defer fake.Close()
	roots := x509.NewCertPool()
	roots.AddCert(real.Certificate())
	oldRoots, oldEnds, oldSites := RootCAs, TestEndpoints, InterceptSites
	defer func() { RootCAs, TestEndpoints, InterceptSites = oldRoots, oldEnds, oldSites }()
	RootCAs = roots
	proxy := connectProxy(t)
	ctx := context.Background()

	TestEndpoints = []string{real.URL + "/cdn-cgi/trace"}
	if r := Probe(ctx, proxy, 5*time.Second); !r.OK || r.Tampered {
		t.Fatalf("genuine certificate: %+v", r)
	}
	TestEndpoints = []string{fake.URL + "/cdn-cgi/trace"}
	if r := Probe(ctx, proxy, 5*time.Second); r.OK || !r.Tampered {
		t.Fatalf("forged certificate not caught: %+v", r)
	}

	InterceptSites = []string{real.URL + "/"}
	if Intercepted(ctx, proxy, 5*time.Second) {
		t.Fatal("a genuine site counted as intercepted")
	}
	InterceptSites = []string{fake.URL + "/"}
	if !Intercepted(ctx, proxy, 5*time.Second) {
		t.Fatal("selective interception not caught")
	}
	// A dead site is no evidence either way.
	InterceptSites = []string{"https://127.0.0.1:1/"}
	if Intercepted(ctx, proxy, 2*time.Second) {
		t.Fatal("a failed connection counted as interception")
	}
}

func TestExpiredIsNotForged(t *testing.T) {
	// A device with a wrong clock sees every certificate as expired.
	if forged(x509.CertificateInvalidError{Reason: x509.Expired}) {
		t.Fatal("expired counted as forged")
	}
	if !forged(x509.UnknownAuthorityError{}) || !forged(x509.HostnameError{}) {
		t.Fatal("forged certificates not recognised")
	}
	if forged(errors.New("connection reset")) {
		t.Fatal("a network error counted as forged")
	}
}

// selfSigned is a certificate for 127.0.0.1 that nobody vouches for, like
// the one an intercepting server makes up.
func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
