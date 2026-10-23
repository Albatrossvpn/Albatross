package engine

import (
	"errors"
	"testing"

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
	// Up to date: nothing to show, connecting is allowed again.
	e.applyUpdate(UpdateManifest{MinVersion: "0.2.1", LatestVersion: "0.2.1"})
	if u := e.Snapshot().Update; u != nil {
		t.Fatalf("up to date: %+v", u)
	}
	// Builds without a release version are never held back.
	e.opt.Version = "dev"
	e.applyUpdate(UpdateManifest{MinVersion: "9.9.9", LatestVersion: "9.9.9"})
	if u := e.Snapshot().Update; u != nil {
		t.Fatalf("dev build: %+v", u)
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
