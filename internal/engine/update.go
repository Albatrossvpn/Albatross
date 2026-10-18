package engine

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cezomagoanlace29861-jpg/vp/internal/sources"
)

// UpdateURL is the manifest that says which versions may still connect.
// Editing update.json on the main branch reaches every installed copy
// within hours (GitHub's CDN mirrors cache it); see the README.
var UpdateURL = "https://raw.githubusercontent.com/cezomagoanlace29861-jpg/vp/main/update.json"

const releasesURL = "https://github.com/cezomagoanlace29861-jpg/vp/releases/latest"

// ErrUpdateRequired means this version is older than the manifest's
// minimum and may not connect any more.
var ErrUpdateRequired = errors.New("update required")

// UpdateManifest is update.json.
type UpdateManifest struct {
	// MinVersion is the oldest version that may still connect.
	MinVersion string `json:"min_version"`
	// LatestVersion, when newer than this one, is offered to the user.
	LatestVersion string `json:"latest_version"`
	// Message is shown with the offer or the requirement.
	Message string `json:"message,omitempty"`
	// DownloadURL is where the new version is (https only).
	DownloadURL string `json:"download_url,omitempty"`
}

// UpdateInfo tells the UI about a newer version.
type UpdateInfo struct {
	Latest   string `json:"latest"`
	Required bool   `json:"required"` // this version may no longer connect
	Message  string `json:"message,omitempty"`
	URL      string `json:"url"`
}

// checkUpdate downloads the manifest (through the VPN when connected, else
// directly or through GitHub's CDN mirrors) and applies it. The last one
// is kept on disk, so a requirement holds offline too.
func (e *Engine) checkUpdate() {
	if UpdateURL == "" {
		return
	}
	res := e.fetcher.Fetch(e.ctx, sources.Source{ID: "__update__", Kind: sources.KindSubscription, URL: UpdateURL})
	var m UpdateManifest
	if res.Text == "" || json.Unmarshal([]byte(res.Text), &m) != nil {
		return
	}
	saveJSON(filepath.Join(e.dir, "update.json"), m)
	e.applyUpdate(m)
}

func (e *Engine) loadUpdate() {
	b, err := os.ReadFile(filepath.Join(e.dir, "update.json"))
	var m UpdateManifest
	if err == nil && json.Unmarshal(b, &m) == nil {
		e.applyUpdate(m)
	}
}

func (e *Engine) applyUpdate(m UpdateManifest) {
	var info *UpdateInfo
	cur, ok := parseVersion(e.opt.Version)
	// Builds without a release version (dev, tests) are never held back.
	if ok {
		if min, ok := parseVersion(m.MinVersion); ok && versionLess(cur, min) {
			info = &UpdateInfo{Required: true}
		} else if latest, ok := parseVersion(m.LatestVersion); ok && versionLess(cur, latest) {
			info = &UpdateInfo{}
		}
	}
	if info != nil {
		info.Latest, info.Message, info.URL = m.LatestVersion, m.Message, releasesURL
		if strings.HasPrefix(m.DownloadURL, "https://") {
			info.URL = m.DownloadURL
		}
	}
	e.mu.Lock()
	was := e.update
	e.update = info
	e.mu.Unlock()
	if info != nil && info.Required && (was == nil || !was.Required) {
		e.logf("warn", "این نسخه دیگر پشتیبانی نمی‌شود؛ برای اتصال، نسخهٔ جدید را نصب کنید.")
		e.Disconnect()
		e.setStatus(StatusIdle, "برای اتصال، نسخهٔ جدید را نصب کنید.")
	}
}

// updateRequired reports whether this version may no longer connect.
func (e *Engine) updateRequired() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.update != nil && e.update.Required
}

// parseVersion reads the leading "major.minor.patch" of v ("0.2.1",
// "0.2.1-dev.3"); anything else is not a release version.
func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	core, _, _ := strings.Cut(strings.TrimPrefix(v, "v"), "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func versionLess(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
