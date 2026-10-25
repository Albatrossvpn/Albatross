package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cezomagoanlace29861-jpg/vp/internal/engine"
	"github.com/cezomagoanlace29861-jpg/vp/internal/sources"
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
	b, _ := json.Marshal(publicSettingsOf(engine.Settings{AllowLAN: true, HelpOthers: true, ShareStats: true, OverrideGPS: true}))
	var m map[string]any
	json.Unmarshal(b, &m)
	for _, k := range []string{"allow_lan", "help_others", "share_stats", "override_gps"} {
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
