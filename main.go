// Orbot for Windows: a desktop counterpart of Orbot (Android) that routes
// Windows traffic through Tor, with bridges for censored networks.
package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

//go:embed ui
var uiFiles embed.FS

var version = "dev"

func appDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return filepath.Dir(exe)
}

func dataDir() string {
	base, err := os.UserConfigDir() // %APPDATA% on Windows
	if err != nil {
		base = appDir()
	}
	return filepath.Join(base, "OrbotWindows")
}

func main() {
	autoConnect := false
	for _, a := range os.Args[1:] {
		if a == "--connect" {
			autoConnect = true
		}
	}
	if !acquireSingleInstance() {
		return
	}
	dd := dataDir()
	_ = os.MkdirAll(dd, 0o700)

	logs := newLogBuffer(800)
	logs.Add("Orbot for Windows %s", version)
	settings := loadSettings(dd)

	paths, torErr := findTor(appDir(), dd)
	if torErr != nil {
		logs.Add("%v", torErr)
	} else {
		logs.Add("tor: %s", paths.TorExe)
	}
	ctl := &Controller{settings: settings, tor: newTor(paths, logs), torErr: torErr, log: logs}
	// Repair the system proxy if a previous run did not exit cleanly.
	ctl.restoreProxy()

	var once sync.Once
	shutdown := func() {
		once.Do(func() {
			ctl.Disconnect()
			logs.Add("bye")
		})
	}

	token := randomToken()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	srv := &http.Server{Handler: newAPI(ctl, settings, logs, token, func() {
		shutdown()
		os.Exit(0)
	})}
	go func() { _ = srv.Serve(ln) }()

	if autoConnect {
		go func() {
			if err := ctl.Connect(); err != nil {
				ctl.setError(err)
			}
		}()
	}

	url := fmt.Sprintf("http://%s/?t=%s", ln.Addr().String(), token)
	runWindow(url, shutdown)
	shutdown()
}

func randomToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newAPI(ctl *Controller, settings *SettingsStore, logs *LogBuffer, token string, quit func()) http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(uiFiles, "ui")
	mux.Handle("/", http.FileServer(http.FS(sub)))

	writeJSON := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	fail := func(w http.ResponseWriter, err error) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
	}
	// Every API call must carry the per-launch token, so web pages open in
	// a browser cannot drive the app through 127.0.0.1.
	api := func(method string, h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Orbot-Token") != token {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			if r.Method != method {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			h(w, r)
		}
	}

	mux.HandleFunc("/api/status", api("GET", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, ctl.Status())
	}))
	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			api("GET", func(w http.ResponseWriter, r *http.Request) {
				s := settings.Get()
				s.SavedProxy = nil
				writeJSON(w, s)
			})(w, r)
			return
		}
		api("POST", func(w http.ResponseWriter, r *http.Request) {
			var in Settings
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
				fail(w, err)
				return
			}
			err := settings.Update(func(s *Settings) {
				saved := s.SavedProxy
				*s = in
				s.SavedProxy = saved
			})
			if err != nil {
				fail(w, err)
				return
			}
			writeJSON(w, map[string]bool{"ok": true})
		})(w, r)
	})
	mux.HandleFunc("/api/connect", api("POST", func(w http.ResponseWriter, r *http.Request) {
		if err := ctl.Connect(); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/disconnect", api("POST", func(w http.ResponseWriter, r *http.Request) {
		go ctl.Disconnect()
		writeJSON(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/newnym", api("POST", func(w http.ResponseWriter, r *http.Request) {
		if err := ctl.NewIdentity(); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/elevate", api("POST", func(w http.ResponseWriter, r *http.Request) {
		ctl.Disconnect()
		if err := relaunchElevated(); err != nil {
			fail(w, errors.New("elevation cancelled"))
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
		go quit()
	}))
	mux.HandleFunc("/api/quit", api("POST", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]bool{"ok": true})
		go quit()
	}))
	mux.HandleFunc("/api/logs", api("GET", func(w http.ResponseWriter, r *http.Request) {
		since, _ := strconv.Atoi(r.URL.Query().Get("since"))
		lines, seq := logs.Since(since)
		writeJSON(w, map[string]any{"lines": lines, "seq": seq})
	}))
	mux.HandleFunc("/api/info", api("GET", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"version": version, "dataDir": dataDir(), "appDir": appDir()})
	}))
	return securityHeaders(mux)
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Refuse DNS-rebinding style requests that name another host.
		host := r.Host
		if i := strings.LastIndex(host, ":"); i > 0 {
			host = host[:i]
		}
		if host != "127.0.0.1" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}
