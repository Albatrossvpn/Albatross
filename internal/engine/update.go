package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/cezomagoanlace29861-jpg/vp/internal/sources"
)

// UpdateURL is the manifest that says which versions may still connect.
// Editing update.json on the main branch reaches every installed copy
// within hours (GitHub's CDN mirrors cache it); see the README.
var UpdateURL = "https://raw.githubusercontent.com/cezomagoanlace29861-jpg/Albatross/main/update.json"

const releasesURL = "https://github.com/cezomagoanlace29861-jpg/Albatross/releases/latest"

// ErrUpdateRequired means this version is older than the manifest's
// minimum and may not connect any more.
var ErrUpdateRequired = errors.New("update required")

// UpdateManifest is update.json.
type UpdateManifest struct {
	// MinVersion is the oldest version that may still connect.
	MinVersion string `json:"min_version"`
	// Require is the oldest version that may still connect, read only by
	// versions that download updates themselves (0.3.2 and later), which
	// fetch the new one through their own connection before they stop.
	// Older versions read only MinVersion, which stays low so they are not
	// cut off where the download site is filtered.
	Require string `json:"require,omitempty"`
	// LatestVersion, when newer than this one, is offered to the user.
	LatestVersion string `json:"latest_version"`
	// Message is shown with the offer or the requirement.
	Message string `json:"message,omitempty"`
	// DownloadURL is where the new version is (https only).
	DownloadURL string `json:"download_url,omitempty"`
	// Files are the new version's installers by platform (updateKey), which
	// the app downloads and opens itself.
	Files map[string]UpdateFile `json:"files,omitempty"`
}

// UpdateFile is one platform's installer: where to get it, in order, and
// its SHA-256, which must match before it is opened.
type UpdateFile struct {
	URLs   []string `json:"urls"`
	SHA256 string   `json:"sha256"`
}

// UpdateInfo tells the UI about a newer version.
type UpdateInfo struct {
	Latest   string `json:"latest"`
	Required bool   `json:"required"` // this version may no longer connect
	Message  string `json:"message,omitempty"`
	URL      string `json:"url"`
	// InApp says the app downloads and installs the new version itself;
	// Progress is the download's percentage while it runs.
	InApp    bool   `json:"in_app"`
	Progress int    `json:"progress"`
	Error    string `json:"error,omitempty"`
	file     *UpdateFile
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
	// Builds without a release version (dev, tests: "0.1.0-dev.12") are
	// never held back.
	if ok && !strings.Contains(e.opt.Version, "-") {
		if requiredFrom(cur, m.MinVersion, m.Require) {
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
		if f, ok := m.Files[updateKey(e.opt.Mobile)]; ok && len(f.URLs) > 0 && len(f.SHA256) == 64 && e.opt.InstallUpdate != nil {
			info.InApp, info.file = true, &f
		}
	}
	e.mu.Lock()
	was := e.update
	if was != nil && info != nil && was.Latest == info.Latest {
		info.Progress, info.Error = was.Progress, was.Error
	}
	e.update = info
	e.mu.Unlock()
	if info != nil && info.Required && (was == nil || !was.Required) {
		e.logf("warn", "This version is no longer supported; install the new version to connect.")
		if info.InApp {
			// Fetched while the connection still stands, so a network
			// that blocks the download site does not strand the user: the
			// app disconnects only once the new version is downloaded and
			// its installer open, and until then it keeps (or may make) a
			// connection to retry through.
			go e.forceUpdate(info.Latest)
			return
		}
		e.Disconnect()
		e.setStatus(StatusIdle, "Install the new version to connect.")
	}
}

// requiredFrom reports whether cur is older than any of the minimums.
func requiredFrom(cur [3]int, mins ...string) bool {
	for _, v := range mins {
		if min, ok := parseVersion(v); ok && versionLess(cur, min) {
			return true
		}
	}
	return false
}

// ErrNoInAppUpdate means this build or platform has no installer in the
// manifest; the UI opens the download page instead.
var ErrNoInAppUpdate = errors.New("no in-app update")

// updateKey names this platform's entry in UpdateManifest.Files.
func updateKey(mobile bool) string {
	if mobile {
		switch runtime.GOARCH {
		case "amd64":
			return "android-x86_64"
		case "arm64":
			return "android-arm64-v8a"
		case "arm":
			return "android-armeabi-v7a"
		}
		return "android-" + runtime.GOARCH // no installer is published
	}
	return "windows"
}

// GetUpdate downloads the new version's installer (through the VPN while
// connected, else directly), checks its SHA-256 and opens it. It returns at
// once; Snapshot().Update reports the progress.
func (e *Engine) GetUpdate() error {
	e.mu.Lock()
	u := e.update
	if u == nil || !u.InApp {
		e.mu.Unlock()
		return ErrNoInAppUpdate
	}
	if e.updating {
		e.mu.Unlock()
		return nil
	}
	e.updating = true
	f, version := *u.file, u.Latest
	u.Progress, u.Error = 0, ""
	e.mu.Unlock()
	path, err := e.downloadUpdate(f, version)
	if err == nil {
		err = e.opt.InstallUpdate(path)
	}
	e.mu.Lock()
	e.updating = false
	if e.update != nil && e.update.Latest == version {
		if err != nil {
			e.update.Error = "The download failed; tap to try again."
		} else {
			e.update.Progress = 100
		}
	}
	e.mu.Unlock()
	if err != nil {
		e.logf("warn", "Downloading version %s failed: %v", version, err)
	}
	return err
}

// directUpdateClient fetches the installer outside the VPN. Tests replace it.
var directUpdateClient = func() *http.Client { return &http.Client{} }

func (e *Engine) downloadUpdate(f UpdateFile, version string) (string, error) {
	ext := ".exe"
	if e.opt.Mobile {
		ext = ".apk"
	}
	dir := filepath.Join(e.dir, "update")
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "Albatross-"+version+ext)
	want := strings.ToLower(f.SHA256)
	if sha256File(path) == want {
		return path, nil
	}
	var clients []*http.Client
	if c := e.proxyClient(); c != nil {
		clients = append(clients, c)
	}
	clients = append(clients, directUpdateClient())
	err := errors.New("no address")
	for _, c := range clients {
		for _, u := range f.URLs {
			if !strings.HasPrefix(u, "https://") {
				continue
			}
			// A cut line resumes where it stopped (fetchUpdate keeps the
			// partial file), so a few tries finish a large file.
			for try := 0; try < 4; try++ {
				if err = e.fetchUpdate(c, u, path, version); err == nil || e.ctx != nil && e.ctx.Err() != nil {
					break
				}
			}
			if err != nil {
				continue
			}
			if sha256File(path) == want {
				return path, nil
			}
			os.Remove(path)
			err = errors.New("the file does not match its checksum")
		}
	}
	return "", err
}

func (e *Engine) fetchUpdate(c *http.Client, u, path, version string) error {
	parent := e.ctx
	if parent == nil { // not started (tests)
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Minute)
	defer cancel()
	part := path + ".part"
	var have int64
	if fi, err := os.Stat(part); err == nil {
		have = fi.Size()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if have > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(have, 10)+"-")
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	flags, total := os.O_CREATE|os.O_WRONLY|os.O_TRUNC, resp.ContentLength
	switch {
	case resp.StatusCode == http.StatusPartialContent && have > 0:
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
		if total > 0 {
			total += have
		}
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && have > 0:
		// The partial file is already whole; the checksum decides.
		return os.Rename(part, path)
	case resp.StatusCode == http.StatusOK:
		have = 0
	default:
		return errors.New(resp.Status)
	}
	tmp, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	done := have
	buf := make([]byte, 64<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := tmp.Write(buf[:n]); err != nil {
				tmp.Close()
				return err
			}
			done += int64(n)
			if total > 0 {
				e.mu.Lock()
				if e.update != nil && e.update.Latest == version {
					e.update.Progress = int(done * 99 / total)
				}
				e.mu.Unlock()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			// The partial file stays for the next try to resume.
			tmp.Close()
			return rerr
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(part, path)
}

// sha256File is the lowercase hex SHA-256 of a file, or "" if unreadable.
func sha256File(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// forceUpdate downloads a required new version, retrying every few
// minutes (through the VPN once one is up), and disconnects once its
// installer is open.
func (e *Engine) forceUpdate(version string) {
	for {
		err := e.GetUpdate()
		e.mu.Lock()
		u := e.update
		e.mu.Unlock()
		if u == nil || !u.Required || u.Latest != version {
			return // a newer manifest took over
		}
		if err == nil {
			e.Disconnect()
			e.setStatus(StatusIdle, "Install the new version to connect.")
			return
		}
		done := context.Background().Done()
		if e.ctx != nil {
			done = e.ctx.Done()
		}
		select {
		case <-done:
			return
		case <-time.After(updateRetry):
		}
	}
}

// updateRetry is how long forceUpdate waits after a failed download.
var updateRetry = 3 * time.Minute

// updateRequired reports whether this version may no longer connect. A
// version that downloads its update itself may still connect until the
// new one is downloaded, so the download can go through the VPN.
func (e *Engine) updateRequired() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	u := e.update
	return u != nil && u.Required && (!u.InApp || u.Progress == 100)
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

// updateCopy is e.update for the UI, safe from the download's changes.
// Callers hold e.mu.
func (e *Engine) updateCopy() *UpdateInfo {
	if e.update == nil {
		return nil
	}
	u := *e.update
	return &u
}
