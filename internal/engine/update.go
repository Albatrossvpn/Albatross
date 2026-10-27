package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Albatrossvpn/vp/internal/sources"
	"github.com/Albatrossvpn/vp/internal/updatesig"
)

// UpdateURL is the manifest that says which versions may still connect.
// Editing update.json on the main branch reaches every installed copy
// within hours (GitHub's CDN mirrors cache it); see the README.
var UpdateURL = "https://raw.githubusercontent.com/Albatrossvpn/Albatross/main/update.json"

// UpdateKey is the hex Ed25519 public key the update manifest must be
// signed with (internal/updatesig), set at build time with
// -X .../internal/engine.UpdateKey=<hex>. Empty (development builds and
// tests) accepts unsigned manifests.
var UpdateKey = ""

const releasesURL = "https://github.com/Albatrossvpn/Albatross/releases/latest"

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
	// Signed is the manifest as signed (base64 of its JSON) and Sig the
	// signature (updatesig). A build with UpdateKey trusts only those
	// contents; the plain fields above serve copies before 1.0.0.
	Signed string `json:"signed,omitempty"`
	Sig    string `json:"sig,omitempty"`
}

// verifyManifest returns the manifest to trust. With UpdateKey set, that is
// the signed payload's contents (and false when the signature is missing or
// wrong); without a key, the manifest as given.
func verifyManifest(m UpdateManifest) (UpdateManifest, bool) {
	if UpdateKey == "" {
		return m, true
	}
	payload, err := updatesig.Verify(UpdateKey, m.Signed, m.Sig)
	if err != nil {
		return UpdateManifest{}, false
	}
	var inner UpdateManifest
	if json.Unmarshal(payload, &inner) != nil {
		return UpdateManifest{}, false
	}
	inner.Signed, inner.Sig = m.Signed, m.Sig
	return inner, true
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
	// Progress is the download's percentage while it runs (1 from the
	// moment it starts, so the user sees it is at work), and Step what it
	// is doing when there is no percentage to show: connecting, trying
	// another address, how much came through a line whose length is unknown.
	InApp    bool   `json:"in_app"`
	Progress int    `json:"progress"`
	Step     string `json:"step,omitempty"`
	Error    string `json:"error,omitempty"`
	file     *UpdateFile
}

// setUpdateStep tells the UI what the download of version is doing.
func (e *Engine) setUpdateStep(version, step string) {
	e.mu.Lock()
	if e.update != nil && e.update.Latest == version {
		e.update.Step = step
		if e.update.Progress == 0 {
			e.update.Progress = 1
		}
	}
	e.mu.Unlock()
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
	m, ok := verifyManifest(m)
	if !ok {
		// A forged or damaged manifest changes nothing: the last good one
		// stays in force.
		e.logf("warn", "The update manifest is not signed by the project's key; ignored.")
		return
	}
	saveJSON(filepath.Join(e.dir, "update.json"), m)
	e.applyUpdate(m)
}

// updateEvery is how often the app looks for a new version while it runs,
// and updateLook how old that look may be when the user has the app open:
// a release shows within the hour, and at once to whoever is looking.
const (
	updateEvery = time.Hour
	updateLook  = 10 * time.Minute
)

// checkUpdateIf fetches the manifest when the last fetch is older than age
// and none is running.
func (e *Engine) checkUpdateIf(age time.Duration) {
	e.mu.Lock()
	due := !e.checking && time.Since(e.updateAt) > age
	if due {
		e.checking, e.updateAt = true, time.Now()
	}
	e.mu.Unlock()
	if !due {
		return
	}
	go func() {
		defer func() {
			e.mu.Lock()
			e.checking = false
			e.mu.Unlock()
		}()
		e.checkUpdate()
	}()
}

// Looked tells the engine that the user has the app open (the UI asks for
// its state while it shows), so a new version is looked for unless that
// was done lately.
func (e *Engine) Looked() { e.checkUpdateIf(updateLook) }

func (e *Engine) loadUpdate() {
	b, err := os.ReadFile(filepath.Join(e.dir, "update.json"))
	var m UpdateManifest
	ok := err == nil && json.Unmarshal(b, &m) == nil
	if ok {
		m, ok = verifyManifest(m)
	}
	if !ok {
		m = UpdateManifest{}
	}
	// Before applyUpdate, which may start a download.
	e.removeOldUpdates(m.LatestVersion)
	if ok {
		e.applyUpdate(m)
	}
}

// removeOldUpdates deletes the installers of this version and older ones,
// about 135 MB each, and partial downloads no try will resume: those of
// such versions and of any version but latest, the one on offer. A newer
// version's installer stays until it is installed.
func (e *Engine) removeOldUpdates(latest string) {
	cur, ok := parseVersion(e.opt.Version)
	if !ok {
		return
	}
	dir := filepath.Join(e.dir, "update")
	entries, _ := os.ReadDir(dir)
	for _, d := range entries {
		// Named by downloadUpdate: Albatross-<version>.apk or .exe, plus
		// .part while downloading.
		name, part := strings.CutSuffix(d.Name(), ".part")
		v, mine := strings.CutPrefix(strings.TrimSuffix(strings.TrimSuffix(name, ".apk"), ".exe"), "Albatross-")
		ver, ok := parseVersion(v)
		if !mine || !ok {
			continue
		}
		if !versionLess(cur, ver) || part && latest != "" && v != latest {
			os.Remove(filepath.Join(dir, d.Name()))
		}
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
		// Only a plain release version is downloaded (downloadUpdate);
		// otherwise the download page is offered.
		_, release := releaseName(m.LatestVersion)
		if f, ok := m.Files[updateKey(e.opt.Mobile)]; ok && release && len(f.URLs) > 0 && len(f.SHA256) == 64 && e.opt.InstallUpdate != nil {
			info.InApp, info.file = true, &f
		}
	}
	e.mu.Lock()
	was := e.update
	if was != nil && info != nil && was.Latest == info.Latest {
		info.Progress, info.Error = was.Progress, was.Error
	}
	e.update = info
	forcing := e.forcing
	if info != nil && info.Required && info.InApp && !forcing {
		// One retry loop at a time; a newer forced version is picked up
		// by the running loop.
		e.forcing = true
	}
	e.mu.Unlock()
	if info != nil && info.Required && (was == nil || !was.Required) {
		e.logf("warn", "This version is no longer supported; install the new version to connect.")
	}
	if info != nil && info.Required && !(was != nil && was.Required && !info.InApp) {
		if info.InApp {
			if forcing {
				return // the running loop follows the manifest
			}
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

// errUpdating means GetUpdate is already downloading; the caller may try
// again later.
var errUpdating = errors.New("already downloading")

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
		return errUpdating
	}
	e.updating = true
	f, version := *u.file, u.Latest
	u.Progress, u.Error, u.Step = 1, "", "Starting the download…"
	e.mu.Unlock()
	path, err := e.downloadUpdate(f, version)
	downloaded := err == nil
	if downloaded {
		err = e.opt.InstallUpdate(path)
	}
	e.mu.Lock()
	e.updating = false
	if e.update != nil && e.update.Latest == version {
		// Progress is reset on failure: left between 0 and 100 it shows as
		// still downloading. The platform shows nothing itself, since a
		// forced update retries in the background every few minutes.
		e.update.Step = ""
		switch {
		case err == nil:
			e.update.Progress = 100
		case downloaded:
			e.update.Progress, e.update.Error = 0, "The new version could not be opened; it is also on the download page."
		default:
			e.update.Progress, e.update.Error = 0, "The download failed."
		}
	}
	e.mu.Unlock()
	if err != nil && downloaded {
		e.logf("warn", "Opening version %s failed: %v", version, err)
	} else if err != nil {
		e.logf("warn", "Downloading version %s failed: %v", version, err)
	}
	return err
}

// directUpdateClient fetches the installer outside the VPN. Tests replace it.
var directUpdateClient = func() *http.Client { return &http.Client{} }

// A try gives up after updateHeaders without an answer, updateIdle
// without a byte or updateTry in all, and the next one resumes where it
// stopped. The cap ends a slow try too, so a download begun outside the
// VPN takes the VPN in a later round once it is up. Tests shorten them.
var updateHeaders, updateIdle, updateTry = 30 * time.Second, time.Minute, 20 * time.Minute

// errStalled ends a try that received nothing for updateIdle.
var errStalled = errors.New("the download stalled")

// withHeaderTimeout is c giving up on a server that takes the request but
// never answers it.
func withHeaderTimeout(c *http.Client) *http.Client {
	t, ok := c.Transport.(*http.Transport)
	if c.Transport == nil {
		t, ok = http.DefaultTransport.(*http.Transport)
	}
	if !ok {
		return c
	}
	t = t.Clone()
	t.ResponseHeaderTimeout = updateHeaders
	cc := *c
	cc.Transport = t
	return &cc
}

// releaseName is version rebuilt from its numbers when it is a plain
// release version (1.2.3), and false for anything else (v1.2.3, 1.2.3-rc,
// or text with a path in it).
func releaseName(version string) (string, bool) {
	v, ok := parseVersion(version)
	name := fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
	return name, ok && name == version
}

func (e *Engine) downloadUpdate(f UpdateFile, version string) (string, error) {
	// The installer is named by its version, so the name is built from the
	// version's numbers: whatever a manifest says, it cannot lead the file
	// out of the update folder.
	name, ok := releaseName(version)
	if !ok {
		return "", fmt.Errorf("%q is not a release version", version)
	}
	ext := ".exe"
	if e.opt.Mobile {
		ext = ".apk"
	}
	dir := filepath.Join(e.dir, "update")
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "Albatross-"+name+ext)
	want := strings.ToLower(f.SHA256)
	if sha256File(path) == want {
		return path, nil
	}
	e.mu.Lock()
	bad := e.badDownloads[version]
	e.mu.Unlock()
	if bad >= 2 {
		return "", errors.New("the downloaded file did not match its checksum twice; waiting for a new release")
	}
	var clients []*http.Client
	if c := e.proxyClient(); c != nil {
		clients = append(clients, c)
	}
	direct := directUpdateClient()
	clients = append(clients, direct)
	err := errors.New("no address")
	// Mirrors inside the country serve the installers too, by name: last
	// as usual, first where foreign addresses are cut. Plain http is fine
	// for them alone: the file is checked against its SHA-256.
	urls := append([]string(nil), f.URLs...)
	var dom []string
	for _, u := range f.URLs {
		for _, b := range sources.DomesticBases() {
			dom = append(dom, b+"/dl/"+filepath.Base(u))
		}
	}
	if e.fetchMode() == sources.ModeDomestic {
		urls = append(dom, urls...)
	} else {
		urls = append(urls, dom...)
	}
	attempt := 0
	for _, c := range clients {
		isDirect := c == direct
		c = withHeaderTimeout(c)
		for _, u := range urls {
			if !strings.HasPrefix(u, "https://") && !sources.IsDomestic(u) {
				continue
			}
			attempt++
			switch {
			case attempt == 1 && !isDirect:
				e.setUpdateStep(version, "Connecting to the download through the VPN…")
			case attempt == 1:
				e.setUpdateStep(version, "Connecting to the download…")
			default:
				e.setUpdateStep(version, fmt.Sprintf("Trying another address (%d)…", attempt))
			}
			uc := c
			if isDirect && sources.IsWorker(u) {
				// The project's own storage shares the account service's
				// name: never a plain direct call (sources.IsWorker).
				if e.privateDirect == nil {
					continue
				}
				uc = withHeaderTimeout(e.privateDirect)
			}
			// A cut line resumes where it stopped (fetchUpdate keeps the
			// partial file), so a few tries finish a large file.
			for try := 0; try < 4; try++ {
				if err = e.fetchUpdate(uc, u, path, version); err == nil || e.ctx != nil && e.ctx.Err() != nil {
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
			e.mu.Lock()
			if e.badDownloads == nil {
				e.badDownloads = map[string]int{}
			}
			e.badDownloads[version]++
			e.mu.Unlock()
			err = errors.New("the file does not match its checksum")
		}
	}
	return "", err
}

func (e *Engine) fetchUpdate(c *http.Client, u, path, version string) (err error) {
	parent := e.ctx
	if parent == nil { // not started (tests)
		parent = context.Background()
	}
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	ctx, stop := context.WithTimeout(ctx, updateTry)
	defer stop()
	idle := time.AfterFunc(updateIdle, func() { cancel(errStalled) })
	defer idle.Stop()
	defer func() {
		if err != nil && errors.Is(context.Cause(ctx), errStalled) {
			err = errStalled
		}
	}()
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
	idle.Reset(updateIdle)
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
			idle.Reset(updateIdle)
			if _, err := tmp.Write(buf[:n]); err != nil {
				tmp.Close()
				return err
			}
			done += int64(n)
			e.mu.Lock()
			if e.update != nil && e.update.Latest == version {
				if total > 0 {
					e.update.Progress, e.update.Step = max(1, int(done*99/total)), ""
				} else if done>>20 != (done-int64(n))>>20 {
					// A line of unknown length: show what came through.
					e.update.Step = fmt.Sprintf("Downloaded %d MB…", done>>20)
				}
			}
			e.mu.Unlock()
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
	defer func() {
		e.mu.Lock()
		e.forcing = false
		e.mu.Unlock()
	}()
	fails := 0
	for {
		err := e.GetUpdate()
		e.mu.Lock()
		u := e.update
		e.mu.Unlock()
		if u == nil || !u.Required || !u.InApp {
			return // the requirement was lifted, or it has no installer
		}
		if u.Latest != version {
			// A newer forced version: the loop follows it.
			version, fails = u.Latest, 0
			continue
		}
		if err == nil {
			e.Disconnect()
			e.setStatus(StatusIdle, "Install the new version to connect.")
			return
		}
		// A download the user started (or an earlier call) is still running:
		// wait for it rather than treating it as done. Failures back off,
		// from a few minutes to two hours: each try may fetch 100 MB
		// through a metered connection.
		wait := updatePoll
		if !errors.Is(err, errUpdating) {
			wait = min(updateRetry<<min(fails, 6), 2*time.Hour)
			fails++
		}
		done := context.Background().Done()
		if e.ctx != nil {
			done = e.ctx.Done()
		}
		select {
		case <-done:
			return
		case <-time.After(wait):
		}
	}
}

// updateRetry is how long forceUpdate waits after a failed download, and
// updatePoll how often it looks again while another download is running.
var updateRetry, updatePoll = 3 * time.Minute, 5 * time.Second

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

// InstallerFile downloads this platform's installer of the newest version
// the app knows of, checked against its SHA-256, and returns its path: for
// passing the app on when its website cannot be reached. The download goes
// through the VPN when it is up, then through each listed address.
func (e *Engine) InstallerFile() (string, error) {
	b, err := os.ReadFile(filepath.Join(e.dir, "update.json"))
	var m UpdateManifest
	if err != nil || json.Unmarshal(b, &m) != nil {
		return "", errors.New("The installer's address is not known yet. Connect once and try again.")
	}
	key := "windows"
	if e.opt.Mobile {
		key = "android-arm64-v8a"
	}
	f, ok := m.Files[key]
	if !ok || len(f.URLs) == 0 || m.LatestVersion == "" {
		return "", errors.New("No installer is listed for this device yet.")
	}
	e.mu.Lock()
	if e.updating {
		e.mu.Unlock()
		return "", errUpdating
	}
	e.updating = true
	e.mu.Unlock()
	path, err := e.downloadUpdate(f, m.LatestVersion)
	e.mu.Lock()
	e.updating = false
	// The download wrote its progress onto the update on offer, which is
	// not downloading: the banner must not stay at "Downloading… N%".
	if e.update != nil && e.update.Latest == m.LatestVersion && e.update.Progress < 100 {
		e.update.Progress = 0
	}
	e.mu.Unlock()
	if err != nil {
		return "", errors.New("The installer could not be downloaded: " + err.Error())
	}
	return path, nil
}
