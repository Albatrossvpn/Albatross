package ui

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/Albatrossvpn/vp/internal/engine"
	"github.com/Albatrossvpn/vp/internal/sources"
)

func TestAPIGuards(t *testing.T) {
	eng, err := engine.New(engine.Options{DataDir: t.TempDir(), Sources: []sources.Source{}})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{eng: eng, token: "secret", host: "127.0.0.1:1234"}
	h := s.routes()
	do := func(host, path, token string) int {
		r := httptest.NewRequest(http.MethodGet, "http://"+host+path, nil)
		r.Host = host
		if token != "" {
			r.Header.Set("X-Token", token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if c := do("127.0.0.1:1234", "/api/state", ""); c != http.StatusForbidden {
		t.Fatalf("no token: %d", c)
	}
	if c := do("127.0.0.1:1234", "/api/state", "wrong"); c != http.StatusForbidden {
		t.Fatalf("wrong token: %d", c)
	}
	if c := do("evil.example:1234", "/api/state", "secret"); c != http.StatusForbidden {
		t.Fatalf("rebinding host: %d", c)
	}
	if c := do("127.0.0.1:1234", "/api/state", "secret"); c != http.StatusOK {
		t.Fatalf("valid request: %d", c)
	}
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1234/", nil)
	r.Host = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Albatross") {
		t.Fatalf("index: %d", w.Code)
	}
}

// Every switch on the settings page must come back in the state, or a
// switch shown off can never be turned off.
func TestSettingsRoundTrip(t *testing.T) {
	b, _ := json.Marshal(publicSettingsOf(engine.Settings{AllowLAN: true, HelpOthers: true, ShareStats: true, OverrideGPS: true, BypassApps: true}))
	var m map[string]any
	json.Unmarshal(b, &m)
	for _, k := range []string{"allow_lan", "help_others", "share_stats", "override_gps", "bypass_apps"} {
		if m[k] != true {
			t.Errorf("%s missing from the page's settings: %s", k, b)
		}
	}
}

// The phone is asked about the GPS override's setup only while the switch
// is on, and the page can open only the screens of its two steps.
func TestLocationSetup(t *testing.T) {
	eng, err := engine.New(engine.Options{DataDir: t.TempDir(), Sources: []sources.Source{}})
	if err != nil {
		t.Fatal(err)
	}
	var opened []string
	s := &Server{eng: eng, token: "secret", host: "127.0.0.1:1234", hooks: Hooks{
		LocationSetup:        func() string { return "mock_app" },
		OpenLocationSettings: func(step string) error { opened = append(opened, step); return nil },
	}}
	h := s.routes()
	do := func(method, path, body string) (int, map[string]any) {
		r := httptest.NewRequest(method, "http://127.0.0.1:1234"+path, strings.NewReader(body))
		r.Host = "127.0.0.1:1234"
		r.Header.Set("X-Token", "secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var m map[string]any
		json.Unmarshal(w.Body.Bytes(), &m)
		return w.Code, m
	}
	if _, st := do(http.MethodGet, "/api/state", ""); st["location_setup"] != nil {
		t.Fatalf("setup asked while the switch is off: %v", st["location_setup"])
	}
	if c, set := do(http.MethodPost, "/api/settings", `{"override_gps": true}`); c != http.StatusOK || set["override_gps"] != true {
		t.Fatalf("turning it on: %d %v", c, set)
	}
	if _, st := do(http.MethodGet, "/api/state", ""); st["location_setup"] != "mock_app" {
		t.Fatalf("location_setup = %v", st["location_setup"])
	}
	if c, _ := do(http.MethodPost, "/api/location-settings", `{"step": "developer"}`); c != http.StatusOK {
		t.Fatalf("opening a step: %d", c)
	}
	if c, _ := do(http.MethodPost, "/api/location-settings", `{"step": "wifi"}`); c != http.StatusBadRequest {
		t.Fatalf("unknown step: %d", c)
	}
	if len(opened) != 1 || opened[0] != "developer" {
		t.Fatalf("opened %v", opened)
	}
	s.hooks = Hooks{}
	if c, _ := do(http.MethodPost, "/api/location-settings", `{"step": "mock_app"}`); c != http.StatusBadRequest {
		t.Fatalf("without the hook: %d", c)
	}
}

// Every call the page makes must reach a handler: a missing route answers
// "404 page not found", which the page shows as the error.
func TestPageCallsServedRoutes(t *testing.T) {
	s := &Server{token: "secret", host: "127.0.0.1:1234"}
	mux := s.mux()
	call := regexp.MustCompile(`api\(\s*["'](GET|POST)["']\s*,\s*["'](/api/[^"']+)["']`)
	n := 0
	fs.WalkDir(webFS, "web", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !(strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".html")) {
			return err
		}
		b, _ := fs.ReadFile(webFS, path)
		for _, m := range call.FindAllStringSubmatch(string(b), -1) {
			n++
			_, pattern := mux.Handler(httptest.NewRequest(m[1], "http://127.0.0.1:1234"+m[2], nil))
			if pattern != m[1]+" "+m[2] {
				t.Errorf("%s calls %s %s, which nothing serves (matched %q)", path, m[1], m[2], pattern)
			}
		}
		return nil
	})
	if n < 10 {
		t.Fatalf("found only %d calls in the page; the pattern no longer matches how it calls the app", n)
	}
}

// signInService stands in for the account Worker.
func signInService(t *testing.T) string {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		account := map[string]any{"username": "ada", "allowed": true, "quota_bytes": 1000}
		switch r.URL.Path {
		case "/api/v1/login":
			var in map[string]string
			json.NewDecoder(r.Body).Decode(&in)
			if in["username"] != "ada" || in["password"] != "pw" {
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{"error": "Wrong username or password"})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"token": "t", "account": account})
		default:
			json.NewEncoder(w).Encode(map[string]any{"account": account})
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

// A build with accounts signs in and out through the page's own calls.
func TestSignInThroughThePage(t *testing.T) {
	eng, err := engine.New(engine.Options{DataDir: t.TempDir(), Sources: []sources.Source{}, AccountURLs: []string{signInService(t)}})
	if err != nil {
		t.Fatal(err)
	}
	h := (&Server{eng: eng, token: "secret", host: "127.0.0.1:1234"}).routes()
	do := func(method, path, body string) (int, map[string]any) {
		r := httptest.NewRequest(method, "http://127.0.0.1:1234"+path, strings.NewReader(body))
		r.Host = "127.0.0.1:1234"
		r.Header.Set("X-Token", "secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var m map[string]any
		json.Unmarshal(w.Body.Bytes(), &m)
		return w.Code, m
	}
	account := func() map[string]any {
		_, st := do(http.MethodGet, "/api/state", "")
		a, _ := st["account"].(map[string]any)
		return a
	}
	if a := account(); a["enabled"] != true || a["signed_in"] != false {
		t.Fatalf("before signing in: %v", a)
	}
	if c, m := do(http.MethodPost, "/api/connect", `{}`); c != http.StatusBadRequest || m["code"] != "login_required" {
		t.Fatalf("connecting without an account: %d %v", c, m)
	}
	if c, m := do(http.MethodPost, "/api/login", `{"username": "ada", "password": "nope"}`); c != http.StatusBadRequest || !strings.Contains(m["error"].(string), "Wrong username") {
		t.Fatalf("a wrong password: %d %v", c, m)
	}
	if c, m := do(http.MethodPost, "/api/login", `{"username": "ada", "password": "pw"}`); c != http.StatusOK {
		t.Fatalf("signing in: %d %v", c, m)
	}
	if a := account(); a["signed_in"] != true || a["username"] != "ada" {
		t.Fatalf("after signing in: %v", a)
	}
	if c, m := do(http.MethodPost, "/api/logout", ""); c != http.StatusOK {
		t.Fatalf("signing out: %d %v", c, m)
	}
	if a := account(); a["signed_in"] != false {
		t.Fatalf("after signing out: %v", a)
	}
}

// A protocol is shown by its name in the protocol list, REALITY named.
func TestProtocolName(t *testing.T) {
	for _, c := range []struct{ proto, security, want string }{
		{"vless", "reality", "VLESS Reality"},
		{"vless", "tls", "VLESS"},
		{"hysteria2", "", "Hysteria 2"},
		{"wireguard", "", "WireGuard"},
		{"mystery", "", "mystery"},
		{"", "", ""},
	} {
		if got := protocolName(c.proto, c.security); got != c.want {
			t.Errorf("protocolName(%q, %q) = %q, want %q", c.proto, c.security, got, c.want)
		}
	}
}
