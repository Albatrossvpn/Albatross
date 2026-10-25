package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Albatrossvpn/vp/internal/sources"
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

// A line that goes silent, before the server answers or halfway through
// the file, is given up within updateIdle and the next try resumes.
func TestUpdateStalls(t *testing.T) {
	body := bytes.Repeat([]byte("albatross"), 50000)
	sum := sha256.Sum256(body)
	var requests atomic.Int32
	var resumed atomic.Bool
	done := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requests.Add(1) {
		case 1: // no answer
		case 2: // half the file, then nothing
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.Write(body[:len(body)/2])
			w.(http.Flusher).Flush()
		default:
			resumed.Store(r.Header.Get("Range") == "bytes="+strconv.Itoa(len(body)/2)+"-")
			http.ServeContent(w, r, "x.apk", time.Time{}, bytes.NewReader(body))
			return
		}
		select {
		case <-r.Context().Done():
		case <-done:
		}
	}))
	defer srv.Close()
	defer close(done)
	old, oldHeaders, oldIdle := directUpdateClient, updateHeaders, updateIdle
	directUpdateClient, updateHeaders, updateIdle = srv.Client, 200*time.Millisecond, 300*time.Millisecond
	defer func() { directUpdateClient, updateHeaders, updateIdle = old, oldHeaders, oldIdle }()

	installed := ""
	e, err := New(Options{DataDir: t.TempDir(), Sources: []sources.Source{}, Version: "0.2.1",
		InstallUpdate: func(path string) error { installed = path; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer e.pool.Close()
	e.applyUpdate(UpdateManifest{LatestVersion: "0.2.2", Files: map[string]UpdateFile{
		updateKey(false): {URLs: []string{srv.URL + "/x.apk"}, SHA256: hex.EncodeToString(sum[:])}}})
	got := make(chan error, 1)
	go func() { got <- e.GetUpdate() }()
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a silent line was not given up")
	}
	if b, _ := os.ReadFile(installed); !bytes.Equal(b, body) || requests.Load() != 3 || !resumed.Load() {
		t.Fatalf("installed %d bytes in %d requests, resumed %v", len(b), requests.Load(), resumed.Load())
	}
}

// A slow download that keeps moving is not taken for stalled.
func TestUpdateSlowButMoving(t *testing.T) {
	body := bytes.Repeat([]byte("albatross"), 5000)
	sum := sha256.Sum256(body)
	var requests atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		for i := 0; i < len(body); i += len(body) / 20 {
			w.Write(body[i:min(i+len(body)/20, len(body))])
			w.(http.Flusher).Flush()
			time.Sleep(25 * time.Millisecond)
		}
	}))
	defer srv.Close()
	old, oldIdle := directUpdateClient, updateIdle
	directUpdateClient, updateIdle = srv.Client, 250*time.Millisecond
	defer func() { directUpdateClient, updateIdle = old, oldIdle }()

	installed := ""
	e, err := New(Options{DataDir: t.TempDir(), Sources: []sources.Source{}, Version: "0.2.1",
		InstallUpdate: func(path string) error { installed = path; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer e.pool.Close()
	e.applyUpdate(UpdateManifest{LatestVersion: "0.2.2", Files: map[string]UpdateFile{
		updateKey(false): {URLs: []string{srv.URL + "/x.apk"}, SHA256: hex.EncodeToString(sum[:])}}})
	start := time.Now()
	if err := e.GetUpdate(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(installed); !bytes.Equal(b, body) || requests.Load() != 1 || time.Since(start) < updateIdle {
		t.Fatalf("installed %d bytes in %d requests after %v", len(b), requests.Load(), time.Since(start))
	}
}

// A try that moves but takes longer than updateTry is cut all the same,
// and the next one resumes it.
func TestUpdateTryCapped(t *testing.T) {
	body := bytes.Repeat([]byte("albatross"), 5000)
	sum := sha256.Sum256(body)
	var requests atomic.Int32
	var resumed atomic.Bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		from := 0
		if s, ok := strings.CutPrefix(r.Header.Get("Range"), "bytes="); ok {
			from, _ = strconv.Atoi(strings.TrimSuffix(s, "-"))
			resumed.Store(from > 0)
			w.Header().Set("Content-Range", "bytes "+strconv.Itoa(from)+"-"+strconv.Itoa(len(body)-1)+"/"+strconv.Itoa(len(body)))
			w.Header().Set("Content-Length", strconv.Itoa(len(body)-from))
			w.WriteHeader(http.StatusPartialContent)
		} else {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		}
		// The whole file takes about half a second.
		for i := from; i < len(body); i += len(body) / 10 {
			if _, err := w.Write(body[i:min(i+len(body)/10, len(body))]); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(50 * time.Millisecond)
		}
	}))
	defer srv.Close()
	old, oldTry := directUpdateClient, updateTry
	directUpdateClient, updateTry = srv.Client, 300*time.Millisecond
	defer func() { directUpdateClient, updateTry = old, oldTry }()

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
	if b, _ := os.ReadFile(installed); !bytes.Equal(b, body) || requests.Load() < 2 || !resumed.Load() {
		t.Fatalf("installed %d bytes in %d requests, resumed %v", len(b), requests.Load(), resumed.Load())
	}
}

// A downloaded update whose installer cannot be opened says so, and does
// not show as still downloading.
func TestUpdateOpenFails(t *testing.T) {
	body := []byte("new version")
	sum := sha256.Sum256(body)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Write(body)
	}))
	defer srv.Close()
	old := directUpdateClient
	directUpdateClient = srv.Client
	defer func() { directUpdateClient = old }()

	e, err := New(Options{DataDir: t.TempDir(), Sources: []sources.Source{}, Version: "0.2.1",
		InstallUpdate: func(string) error { return errors.New("no installer") }})
	if err != nil {
		t.Fatal(err)
	}
	defer e.pool.Close()
	file := UpdateFile{URLs: []string{srv.URL + "/x"}, SHA256: hex.EncodeToString(sum[:])}
	e.applyUpdate(UpdateManifest{LatestVersion: "0.2.2", Files: map[string]UpdateFile{updateKey(false): file}})
	if err := e.GetUpdate(); err == nil {
		t.Fatal("opened")
	}
	if u := e.Snapshot().Update; u.Progress != 0 || !strings.Contains(u.Error, "could not be opened") {
		t.Fatalf("update = %+v", u)
	}
	// A download that fails says that instead.
	file.SHA256 = strings.Repeat("0", 64)
	e.applyUpdate(UpdateManifest{LatestVersion: "0.2.3", Files: map[string]UpdateFile{updateKey(false): file}})
	if err := e.GetUpdate(); err == nil {
		t.Fatal("opened")
	}
	if u := e.Snapshot().Update; u.Progress != 0 || !strings.Contains(u.Error, "download failed") {
		t.Fatalf("update = %+v", u)
	}
}

// At start the installers of this version and older ones go, and partial
// downloads no try will resume; a newer version's installer stays.
func TestOldUpdatesRemoved(t *testing.T) {
	dir := t.TempDir()
	e, err := New(Options{DataDir: dir, Sources: []sources.Source{}, Version: "0.3.4"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.pool.Close()
	up := filepath.Join(dir, "update")
	os.MkdirAll(up, 0o755)
	kept := map[string]bool{
		"Albatross-0.3.3.apk":      false, // older
		"Albatross-0.3.4.exe":      false, // this version
		"Albatross-0.3.2.exe.part": false,
		"Albatross-0.3.5.exe.part": false, // not the version on offer
		"Albatross-0.3.5.apk":      true,  // newer, still to be installed
		"Albatross-0.3.6.apk.part": true,  // on offer: the next try resumes it
		"notes.txt":                true,
	}
	for name := range kept {
		os.WriteFile(filepath.Join(up, name), []byte("x"), 0o644)
	}
	saveJSON(filepath.Join(dir, "update.json"), UpdateManifest{MinVersion: "0.3.0", LatestVersion: "0.3.6"})
	e.loadUpdate()
	for name, want := range kept {
		if _, err := os.Stat(filepath.Join(up, name)); (err == nil) != want {
			t.Errorf("%s: kept %v, want %v", name, err == nil, want)
		}
	}
	// Without a manifest no newer partial download is taken for stale.
	os.Remove(filepath.Join(dir, "update.json"))
	e.loadUpdate()
	if _, err := os.Stat(filepath.Join(up, "Albatross-0.3.6.apk.part")); err != nil {
		t.Error(err)
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

func TestInstallerFile(t *testing.T) {
	body := []byte("MZ installer bytes")
	sum := sha256.Sum256(body)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dead" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write(body)
	}))
	defer srv.Close()
	was := directUpdateClient
	directUpdateClient = srv.Client
	defer func() { directUpdateClient = was }()
	dir := t.TempDir()
	e, err := New(Options{DataDir: dir, Sources: []sources.Source{}, Version: "0.4.0"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if _, err := e.InstallerFile(); err == nil {
		t.Fatal("no manifest yet: expected an error")
	}
	m := UpdateManifest{LatestVersion: "0.4.1", Files: map[string]UpdateFile{
		"windows": {URLs: []string{srv.URL + "/dead", srv.URL + "/Albatross-Setup-0.4.1.exe"}, SHA256: hex.EncodeToString(sum[:])},
	}}
	saveJSON(filepath.Join(dir, "update.json"), m)
	path, err := e.InstallerFile()
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(body) || filepath.Base(path) != "Albatross-0.4.1.exe" {
		t.Fatalf("got %q at %s", got, path)
	}
}
