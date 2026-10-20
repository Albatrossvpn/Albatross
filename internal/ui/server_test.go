package ui

import (
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
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Skyroad") {
		t.Fatalf("index: %d", w.Code)
	}
}
