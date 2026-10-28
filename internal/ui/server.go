// Package ui serves the app's interface (an embedded web page shown in a
// native window) and the local API it talks to.
package ui

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Albatrossvpn/vp/internal/engine"
)

//go:embed web
var webFS embed.FS

// Hooks are app-level actions the page can trigger.
type Hooks struct {
	Quit          func()
	RelaunchAdmin func() error
	OpenURL       func(string) error
	// OpenVPNSettings opens the system's VPN settings (Android), where
	// "Always-on VPN" and "Block connections without VPN" live.
	OpenVPNSettings func() error
	// ShareText hands text to the system share sheet (Android), which
	// reaches Bluetooth and Quick Share as well as messengers.
	ShareText func(string) error
	// ShareApp shares the app's own installer (Android).
	ShareApp func() error
	// SaveInstaller puts this app's installer where the user can pass it
	// on (desktop) and returns that path.
	SaveInstaller func() (string, error)
	// LocationSetup says what the phone still needs before the app may set
	// its location (Android): "" when it is ready, "developer" while
	// Developer options are off, "mock_app" until the app is the selected
	// mock location app.
	LocationSetup func() string
	// OpenLocationSettings opens the system screen for one of those steps.
	OpenLocationSettings func(step string) error
}

// Server is the local UI server. It listens on 127.0.0.1 only and every
// API call must carry a per-run secret token, so other local programs and
// web pages cannot drive it.
type Server struct {
	eng   *engine.Engine
	hooks Hooks
	token string
	host  string
	srv   *http.Server
}

// Start serves the UI on a random local port and returns its URL.
func Start(eng *engine.Engine, hooks Hooks, port int) (*Server, string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return nil, "", err
	}
	b := make([]byte, 16)
	rand.Read(b)
	s := &Server{eng: eng, hooks: hooks, token: hex.EncodeToString(b), host: ln.Addr().String()}
	s.srv = &http.Server{Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}
	go s.srv.Serve(ln)
	return s, "http://" + s.host + "/#" + s.token, nil
}

// Close stops the server.
func (s *Server) Close() { s.srv.Close() }

func (s *Server) routes() http.Handler { return s.checkHost(s.mux()) }

func (s *Server) mux() *http.ServeMux {
	static, _ := fs.Sub(webFS, "web")
	files := http.FileServer(http.FS(static))
	mux := http.NewServeMux()
	mux.Handle("/", noCache(files))
	api := map[string]http.HandlerFunc{
		"GET /api/state":              s.state,
		"GET /api/logs":               s.logs,
		"POST /api/connect":           s.connect,
		"POST /api/login":             s.login,
		"POST /api/logout":            s.logout,
		"POST /api/disconnect":        s.disconnect,
		"POST /api/target":            s.target,
		"POST /api/settings":          s.settings,
		"POST /api/admin":             s.admin,
		"POST /api/open":              s.open,
		"POST /api/update":            s.getUpdate,
		"POST /api/vpn-settings":      s.vpnSettings,
		"POST /api/location-settings": s.locationSettings,
		"POST /api/quit":              s.quit,
		"POST /api/share":             s.share,
		"POST /api/share/import":      s.shareImport,
		"POST /api/share/app":         s.shareApp,
		"POST /api/share/installer":   s.saveInstaller,
	}
	for pattern, h := range api {
		mux.Handle(pattern, s.guard(h))
	}
	return mux
}

// checkHost blocks DNS-rebinding: only requests addressed to our own
// loopback address are served.
func (s *Server) checkHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != s.host {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) guard(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Token")), []byte(s.token)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h(w, r)
	})
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	b, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

type apiError struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
	// For "plan": the feature the plan lacks and the plan that has it.
	Feature string `json:"feature,omitempty"`
	Tier    string `json:"tier,omitempty"`
}

func fail(w http.ResponseWriter, err error) {
	code := ""
	var limit *engine.LimitError
	var plan *engine.PlanError
	out := apiError{Error: err.Error()}
	switch {
	case errors.Is(err, engine.ErrNeedAdmin):
		code = "need_admin"
	case errors.Is(err, engine.ErrLoginRequired):
		code = "login_required"
	case errors.As(err, &limit):
		code = "limit"
	case errors.Is(err, engine.ErrNoInAppUpdate):
		code = "no_in_app"
	case errors.As(err, &plan):
		code, out.Feature, out.Tier = "plan", plan.Feature, plan.Tier
	}
	out.Code = code
	w.WriteHeader(http.StatusBadRequest)
	writeJSON(w, out)
}

func ok(w http.ResponseWriter) { writeJSON(w, map[string]bool{"ok": true}) }

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	// The page asks with look=1 while it shows: whoever opens the app sees
	// a new version without waiting for the hourly look.
	if r.URL.Query().Get("look") == "1" {
		s.eng.Looked()
	}
	st := publicState(s.eng.Snapshot())
	if st.Settings.OverrideGPS && s.hooks.LocationSetup != nil {
		st.LocationSetup = s.hooks.LocationSetup()
	}
	writeJSON(w, st)
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.eng.Logs()) }

func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Country string `json:"country"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	if s.eng.Settings().Mode == "tun" && !s.eng.Snapshot().Admin {
		fail(w, engine.ErrNeedAdmin)
		return
	}
	// The answer for a user who may not connect is given here; the rest of
	// connecting goes on in the background.
	if err := s.eng.AllowConnect(); err != nil {
		fail(w, err)
		return
	}
	// Connecting can take a while (searching); report progress via state.
	go s.eng.Connect(req.Country)
	ok(w)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	if err := s.eng.Login(req.Username, req.Password); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	s.eng.Logout()
	ok(w)
}

// getUpdate starts downloading the new version; the state reports progress.
func (s *Server) getUpdate(w http.ResponseWriter, r *http.Request) {
	if snap := s.eng.Snapshot(); snap.Update == nil || !snap.Update.InApp {
		fail(w, engine.ErrNoInAppUpdate)
		return
	}
	go s.eng.GetUpdate()
	ok(w)
}

func (s *Server) disconnect(w http.ResponseWriter, r *http.Request) {
	s.eng.Disconnect()
	ok(w)
}

func (s *Server) target(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Country string `json:"country"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	if req.Country != "" {
		if err := s.eng.Locked(engine.FeatureLocations); err != nil {
			fail(w, err)
			return
		}
	}
	s.eng.SetTarget(strings.ToUpper(req.Country))
	ok(w)
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	set := s.eng.Settings()
	if err := readJSON(r, &set); err != nil {
		fail(w, err)
		return
	}
	if err := s.eng.UpdateSettings(set); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, publicSettingsOf(s.eng.Settings()))
}

func (s *Server) admin(w http.ResponseWriter, r *http.Request) {
	if s.hooks.RelaunchAdmin == nil {
		fail(w, errors.New("not supported"))
		return
	}
	if err := s.hooks.RelaunchAdmin(); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) open(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := readJSON(r, &req); err != nil || !strings.HasPrefix(req.URL, "https://") {
		fail(w, errors.New("bad url"))
		return
	}
	if s.hooks.OpenURL != nil {
		s.hooks.OpenURL(req.URL)
	}
	ok(w)
}

func (s *Server) vpnSettings(w http.ResponseWriter, r *http.Request) {
	if s.hooks.OpenVPNSettings == nil {
		fail(w, errors.New("not supported"))
		return
	}
	if err := s.hooks.OpenVPNSettings(); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) locationSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Step string `json:"step"`
	}
	if err := readJSON(r, &req); err != nil || (req.Step != "developer" && req.Step != "mock_app") {
		fail(w, errors.New("bad step"))
		return
	}
	if s.hooks.OpenLocationSettings == nil {
		fail(w, errors.New("not supported"))
		return
	}
	if err := s.hooks.OpenLocationSettings(req.Step); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}

func (s *Server) quit(w http.ResponseWriter, r *http.Request) {
	ok(w)
	if s.hooks.Quit != nil {
		go s.hooks.Quit()
	}
}

func (s *Server) share(w http.ResponseWriter, r *http.Request) {
	code, err := s.eng.ShareCode()
	if err != nil {
		fail(w, err)
		return
	}
	sent := s.hooks.ShareText != nil && s.hooks.ShareText(code) == nil
	writeJSON(w, map[string]any{"code": code, "sent": sent})
}

func (s *Server) shareImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	n, err := s.eng.ImportShareCode(req.Code)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"added": n})
}

func (s *Server) saveInstaller(w http.ResponseWriter, r *http.Request) {
	if s.hooks.SaveInstaller == nil {
		fail(w, errors.New("not supported"))
		return
	}
	path, err := s.hooks.SaveInstaller()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"path": path})
}

func (s *Server) shareApp(w http.ResponseWriter, r *http.Request) {
	if s.hooks.ShareApp == nil {
		fail(w, errors.New("not supported"))
		return
	}
	if err := s.hooks.ShareApp(); err != nil {
		fail(w, err)
		return
	}
	ok(w)
}
