package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cezomagoanlace29861-jpg/vp/internal/sources"
)

func TestUpdateManifest(t *testing.T) {
	e, err := New(Options{DataDir: t.TempDir(), Sources: []sources.Source{}, Version: "0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.pool.Close()

	// A newer version is offered, nothing is required.
	e.applyUpdate(UpdateManifest{MinVersion: "0.2.0", LatestVersion: "0.2.2"})
	if u := e.Snapshot().Update; u == nil || u.Required || u.Latest != "0.2.2" || u.URL != releasesURL {
		t.Fatalf("offer: %+v", u)
	}
	// Below the minimum the version may not connect; only https links are used.
	e.applyUpdate(UpdateManifest{MinVersion: "0.3.0", LatestVersion: "0.3.0", DownloadURL: "http://example.com/x.apk"})
	if u := e.Snapshot().Update; u == nil || !u.Required || u.URL != releasesURL {
		t.Fatalf("required: %+v", u)
	}
	if err := e.Connect(""); !errors.Is(err, ErrUpdateRequired) {
		t.Fatalf("connect while outdated: %v", err)
	}
	if err := e.ConnectServer("x"); !errors.Is(err, ErrUpdateRequired) {
		t.Fatalf("connect to a server while outdated: %v", err)
	}
	// Versions that update themselves also honour require.
	e.applyUpdate(UpdateManifest{MinVersion: "0.2.0", Require: "0.2.2", LatestVersion: "0.2.2"})
	if u := e.Snapshot().Update; u == nil || !u.Required {
		t.Fatalf("require: %+v", u)
	}
	// Up to date: nothing to show, connecting is allowed again.
	e.applyUpdate(UpdateManifest{MinVersion: "0.2.1", LatestVersion: "0.2.1"})
	if u := e.Snapshot().Update; u != nil {
		t.Fatalf("up to date: %+v", u)
	}
	// Builds without a release version are never held back.
	for _, v := range []string{"dev", "0.1.0-dev.12"} {
		e.opt.Version = v
		e.applyUpdate(UpdateManifest{MinVersion: "9.9.9", LatestVersion: "9.9.9"})
		if u := e.Snapshot().Update; u != nil {
			t.Fatalf("dev build %s: %+v", v, u)
		}
	}
}

func TestParseVersion(t *testing.T) {
	for v, want := range map[string]bool{"0.2.1": true, "v1.0.0": true, "0.1.0-dev.3": true, "dev": false, "1.2": false, "": false} {
		if _, ok := parseVersion(v); ok != want {
			t.Errorf("parseVersion(%q) ok = %v", v, ok)
		}
	}
	a, _ := parseVersion("0.2.9")
	b, _ := parseVersion("0.10.0")
	if !versionLess(a, b) || versionLess(b, a) {
		t.Error("0.2.9 should be older than 0.10.0")
	}
}

func TestInAppUpdate(t *testing.T) {
	body := []byte("new version")
	sum := sha256.Sum256(body)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			w.Write([]byte("tampered"))
			return
		}
		w.Write(body)
	}))
	defer srv.Close()
	old := directUpdateClient
	directUpdateClient = srv.Client
	defer func() { directUpdateClient = old }()

	installed := ""
	e, err := New(Options{DataDir: t.TempDir(), Sources: []sources.Source{}, Version: "0.2.1",
		InstallUpdate: func(path string) error { installed = path; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer e.pool.Close()
	file := UpdateFile{URLs: []string{srv.URL + "/bad", srv.URL + "/good"}, SHA256: hex.EncodeToString(sum[:])}
	e.applyUpdate(UpdateManifest{LatestVersion: "0.2.2", Files: map[string]UpdateFile{updateKey(false): file}})
	if u := e.Snapshot().Update; u == nil || !u.InApp {
		t.Fatalf("update = %+v, want in-app", u)
	}
	// The tampered copy is refused; the next address's matches.
	if err := e.GetUpdate(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(installed); string(b) != string(body) {
		t.Fatalf("installed %q", b)
	}
	if u := e.Snapshot().Update; u.Progress != 100 || u.Error != "" {
		t.Fatalf("update = %+v", u)
	}

	// Without a matching copy nothing is opened.
	installed = ""
	file.URLs = []string{srv.URL + "/bad"}
	e.applyUpdate(UpdateManifest{LatestVersion: "0.2.3", Files: map[string]UpdateFile{updateKey(false): file}})
	if err := e.GetUpdate(); err == nil || installed != "" {
		t.Fatalf("err = %v, installed %q", err, installed)
	}
}

func TestUpdateResumes(t *testing.T) {
	body := bytes.Repeat([]byte("albatross"), 50000)
	sum := sha256.Sum256(body)
	cuts := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "" {
			// The line is cut halfway through the first download.
			cuts++
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.Write(body[:len(body)/2])
			panic(http.ErrAbortHandler)
		}
		http.ServeContent(w, r, "x.apk", time.Time{}, bytes.NewReader(body))
	}))
	defer srv.Close()
	old := directUpdateClient
	directUpdateClient = srv.Client
	defer func() { directUpdateClient = old }()

	installed := ""
	e, err := New(Options{DataDir: t.TempDir(), Sources: []sources.Source{}, Version: "0.2.1",
		InstallUpdate: func(path string) error { installed = path; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer e.pool.Close()
	e.applyUpdate(UpdateManifest{LatestVersion: "0.2.2", Files: map[string]UpdateFile{
		updateKey(false): {URLs: []string{srv.URL + "/x.apk"}, SHA256: hex.EncodeToString(sum[:])}}})
	if err := e.GetUpdate(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(installed); !bytes.Equal(b, body) || cuts != 1 {
		t.Fatalf("installed %d bytes after %d cuts", len(b), cuts)
	}
}

// A required update that cannot be downloaded must not strand the user:
// connecting stays allowed (so the next try goes through the VPN) until
// the new version is downloaded and its installer open.
func TestForcedUpdateNeverStrands(t *testing.T) {
	body := []byte("new version")
	sum := sha256.Sum256(body)
	var broken atomic.Bool
	broken.Store(true)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if broken.Load() {
			http.Error(w, "filtered", http.StatusBadGateway)
			return
		}
		w.Write(body)
	}))
	defer srv.Close()
	old, oldRetry := directUpdateClient, updateRetry
	directUpdateClient, updateRetry = srv.Client, 50*time.Millisecond
	defer func() { directUpdateClient, updateRetry = old, oldRetry }()

	var installed atomic.Bool
	e, err := New(Options{DataDir: t.TempDir(), Sources: []sources.Source{}, Version: "0.3.3",
		InstallUpdate: func(string) error { installed.Store(true); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer e.pool.Close()
	e.applyUpdate(UpdateManifest{MinVersion: "0.3.0", Require: "0.3.4", LatestVersion: "0.3.4",
		Files: map[string]UpdateFile{updateKey(false): {URLs: []string{srv.URL + "/x"}, SHA256: hex.EncodeToString(sum[:])}}})
	time.Sleep(200 * time.Millisecond)
	if e.updateRequired() || installed.Load() {
		t.Fatal("a failed download blocked connecting")
	}
	// The download site answers again: the retry installs, then it blocks.
	broken.Store(false)
	deadline := time.Now().Add(5 * time.Second)
	for !e.updateRequired() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !installed.Load() || !e.updateRequired() {
		t.Fatalf("installed %v, required %v", installed.Load(), e.updateRequired())
	}
}
