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

	"github.com/cezomagoanlace29861-jpg/vp/internal/engine"
)

//go:embed web
var webFS embed.FS

// Hooks are app-level actions the page can trigger.
type Hooks struct {
	Quit          func()
	RelaunchAdmin func() error
	OpenURL       func(string) error
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

func (s *Server) routes() http.Handler {
	static, _ := fs.Sub(webFS, "web")
	files := http.FileServer(http.FS(static))
	mux := http.NewServeMux()
	mux.Handle("/", noCache(files))
	api := map[string]http.HandlerFunc{
		"GET /api/state":           s.state,
		"GET /api/servers":         s.servers,
		"GET /api/logs":            s.logs,
		"GET /api/sources":         s.sourcesList,
		"POST /api/connect":        s.connect,
		"POST /api/connect-server": s.connectServer,
		"POST /api/disconnect":     s.disconnect,
		"POST /api/target":         s.target,
		"POST /api/settings":       s.settings,
		"POST /api/refresh":        s.refresh,
		"POST /api/import":         s.importText,
		"POST /api/sources/add":    s.sourceAdd,
		"POST /api/sources/remove": s.sourceRemove,
		"POST /api/sources/enable": s.sourceEnable,
		"POST /api/admin":          s.admin,
		"POST /api/open":           s.open,
		"POST /api/quit":           s.quit,
	}
	for pattern, h := range api {
		mux.Handle(pattern, s.guard(h))
	}
	return s.checkHost(mux)
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
}

func fail(w http.ResponseWriter, err error) {
	code := ""
	if errors.Is(err, engine.ErrNeedAdmin) {
		code = "need_admin"
	}
	w.WriteHeader(http.StatusBadRequest)
	writeJSON(w, apiError{Error: err.Error(), Code: code})
}

func ok(w http.ResponseWriter) { writeJSON(w, map[string]bool{"ok": true}) }

func (s *Server) state(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.eng.Snapshot()) }

func (s *Server) servers(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	writeJSON(w, s.eng.Servers(strings.ToUpper(r.URL.Query().Get("country")), limit))
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.eng.Logs()) }

func (s *Server) sourcesList(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.eng.Sources()) }

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
	// Connecting can take a while (searching); report progress via state.
	go s.eng.Connect(req.Country)
	ok(w)
}

func (s *Server) connectServer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key string `json:"key"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	go s.eng.ConnectServer(req.Key)
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
	writeJSON(w, s.eng.Settings())
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	s.eng.Refresh()
	ok(w)
}

func (s *Server) importText(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	text := strings.TrimSpace(req.Text)
	if strings.HasPrefix(text, "http://") || strings.HasPrefix(text, "https://") || strings.HasPrefix(text, "@") || strings.Contains(text, "t.me/") {
		if !strings.ContainsAny(text, "\n ") {
			src, err := s.eng.AddSource(text)
			if err != nil {
				fail(w, err)
				return
			}
			s.eng.Refresh()
			writeJSON(w, map[string]any{"source": src.Label()})
			return
		}
	}
	n, err := s.eng.Import(text)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, map[string]int{"added": n})
}

func (s *Server) sourceAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Input string `json:"input"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	src, err := s.eng.AddSource(req.Input)
	if err != nil {
		fail(w, err)
		return
	}
	s.eng.Refresh()
	writeJSON(w, src)
}

func (s *Server) sourceRemove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	s.eng.RemoveSource(req.ID)
	ok(w)
}

func (s *Server) sourceEnable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, err)
		return
	}
	s.eng.EnableSource(req.ID)
	ok(w)
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

func (s *Server) quit(w http.ResponseWriter, r *http.Request) {
	ok(w)
	if s.hooks.Quit != nil {
		go s.hooks.Quit()
	}
}
