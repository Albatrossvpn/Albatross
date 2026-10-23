package account

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// fakeService stands in for the Worker: one user with a small allowance.
type fakeService struct {
	mu      sync.Mutex
	used    int64
	seconds int64
	quota   int64
	off     bool
	calls   int
}

func (f *fakeService) account() Info {
	i := Info{Username: "ada", Allowed: !f.off, QuotaBytes: f.quota, UsedBytes: f.used, UsedSeconds: f.seconds}
	if f.off {
		i.Reason = ReasonDisabled
	}
	return i
}

func (f *fakeService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/v1/login":
		var in map[string]string
		json.NewDecoder(r.Body).Decode(&in)
		if in["username"] != "ada" || in["password"] != "correct horse" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "Wrong username or password"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"token": "t1", "account": f.account()})
	case "/api/v1/usage":
		if a := r.Header.Get("Authorization"); a != "Bearer t1" && a != "Bearer t2" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "Sign in again"})
			return
		}
		var in map[string]int64
		json.NewDecoder(r.Body).Decode(&in)
		f.used += in["bytes"]
		f.seconds += in["seconds"]
		json.NewEncoder(w).Encode(map[string]any{"token": "t2", "account": f.account()})
	default:
		http.NotFound(w, r)
	}
}

func newTestClient(t *testing.T, urls ...string) (*Client, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "account.json")
	return New(urls, path, nil), path
}

func TestLoginAndSync(t *testing.T) {
	svc := &fakeService{quota: 1000}
	ts := httptest.NewServer(svc)
	defer ts.Close()
	c, path := newTestClient(t, ts.URL)
	ctx := context.Background()

	if _, err := c.Login(ctx, "ada", "wrong"); err == nil {
		t.Fatal("a wrong password signed in")
	} else if he := (*Error)(nil); !errors.As(err, &he) || he.Message != "Wrong username or password" {
		t.Fatalf("login error = %v", err)
	}
	if c.View().SignedIn {
		t.Fatal("signed in after a failed login")
	}
	if _, err := c.Login(ctx, " ada ", "correct horse"); err != nil {
		t.Fatal(err)
	}
	v := c.View()
	if !v.SignedIn || v.Info.Username != "ada" || v.Info.Why(time.Now()) != "" {
		t.Fatalf("view = %+v", v)
	}

	// Use is visible at once, before it is reported.
	c.Add(600, 30)
	if got := c.View().Info.UsedBytes; got != 600 {
		t.Fatalf("used = %d, want 600", got)
	}
	info, err := c.Sync(ctx)
	if err != nil || info.UsedBytes != 600 || svc.used != 600 || svc.seconds != 30 {
		t.Fatalf("sync = %+v, %v (service saw %d bytes, %d s)", info, err, svc.used, svc.seconds)
	}
	// Reported once: a second sync must not count it again.
	if _, err := c.Sync(ctx); err != nil || svc.used != 600 {
		t.Fatalf("second sync: err=%v, service used %d", err, svc.used)
	}
	if got := c.View().Info.UsedBytes; got != 600 {
		t.Fatalf("used after sync = %d, want 600", got)
	}

	// The session survives a restart and is private to the user.
	again := New([]string{ts.URL}, path, nil)
	if !again.View().SignedIn {
		t.Fatal("session not restored")
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("session file mode = %v, %v", fi.Mode(), err)
		}
	}
}

func TestLimitsApplyBetweenSyncs(t *testing.T) {
	svc := &fakeService{quota: 1000}
	ts := httptest.NewServer(svc)
	defer ts.Close()
	c, _ := newTestClient(t, ts.URL)
	if _, err := c.Login(context.Background(), "ada", "correct horse"); err != nil {
		t.Fatal(err)
	}
	c.Add(999, 1)
	if why := c.View().Info.Why(time.Now()); why != "" {
		t.Fatalf("one byte left, but refused: %q", why)
	}
	c.Add(1, 1)
	if why := c.View().Info.Why(time.Now()); why != ReasonQuota {
		t.Fatalf("why = %q, want quota", why)
	}

	exp := Info{Allowed: true, ExpiresAt: time.Now().Add(-time.Hour).Unix()}
	if why := exp.Why(time.Now()); why != ReasonExpired {
		t.Fatalf("expired account: why = %q", why)
	}
	capped := Info{Allowed: true, MaxSeconds: 60, UsedSeconds: 60}
	if why := capped.Why(time.Now()); why != ReasonTime {
		t.Fatalf("time limit: why = %q", why)
	}
	if why := (Info{Allowed: false, Reason: ReasonDisabled}).Why(time.Now()); why != ReasonDisabled {
		t.Fatalf("disabled: why = %q", why)
	}
	if why := (Info{Allowed: true}).Why(time.Now()); why != "" {
		t.Fatalf("unlimited account refused: %q", why)
	}
}

func TestOfflineKeepsUseAndGraceExpires(t *testing.T) {
	svc := &fakeService{}
	ts := httptest.NewServer(svc)
	c, _ := newTestClient(t, ts.URL)
	now := time.Now()
	c.Now = func() time.Time { return now }
	if _, err := c.Login(context.Background(), "ada", "correct horse"); err != nil {
		t.Fatal(err)
	}
	ts.Close() // the service becomes unreachable

	c.Add(500, 10)
	if _, err := c.Sync(context.Background()); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("sync while offline = %v, want ErrUnreachable", err)
	}
	if v := c.View(); !v.SignedIn || v.Info.UsedBytes != 500 || v.Stale {
		t.Fatalf("offline view = %+v", v)
	}
	now = now.Add(Grace + time.Hour)
	if !c.View().Stale {
		t.Fatal("an account not synced for longer than the grace period must be stale")
	}
}

func TestRevokedSessionSignsOut(t *testing.T) {
	svc := &fakeService{}
	ts := httptest.NewServer(svc)
	defer ts.Close()
	c, path := newTestClient(t, ts.URL)
	if _, err := c.Login(context.Background(), "ada", "correct horse"); err != nil {
		t.Fatal(err)
	}
	// Another token than the service knows: it answers 401.
	c.mu.Lock()
	c.st.Token = "stolen"
	c.mu.Unlock()
	if _, err := c.Sync(context.Background()); !errors.Is(err, ErrSignedOut) {
		t.Fatalf("sync = %v, want ErrSignedOut", err)
	}
	if c.View().SignedIn {
		t.Fatal("still signed in after the service refused the session")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("session file left behind")
	}
}

func TestFallsBackToNextAddress(t *testing.T) {
	// A block page (HTML, 200) and a failing address come before the service.
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html>blocked</html>"))
	}))
	defer blocked.Close()
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer broken.Close()
	svc := &fakeService{}
	ts := httptest.NewServer(svc)
	defer ts.Close()
	c, _ := newTestClient(t, blocked.URL, broken.URL, ts.URL)
	if _, err := c.Login(context.Background(), "ada", "correct horse"); err != nil {
		t.Fatalf("login did not reach the service behind the broken addresses: %v", err)
	}
}

func TestAddWhenSignedOutIsIgnored(t *testing.T) {
	c, path := newTestClient(t, "http://127.0.0.1:1")
	c.Add(10, 10)
	if _, err := os.Stat(path); err == nil {
		t.Fatal("use recorded with nobody signed in")
	}
}

// Against a real service (backend/ run with `wrangler dev`), with an account
// that has a small allowance:
//
//	ACCOUNT_TEST_URL=http://127.0.0.1:8787 ACCOUNT_TEST_USER=u ACCOUNT_TEST_PASS=p go test -run RealService ./internal/account
func TestRealService(t *testing.T) {
	url, user, pass := os.Getenv("ACCOUNT_TEST_URL"), os.Getenv("ACCOUNT_TEST_USER"), os.Getenv("ACCOUNT_TEST_PASS")
	if url == "" {
		t.Skip("ACCOUNT_TEST_URL is not set")
	}
	c, _ := newTestClient(t, url)
	ctx := context.Background()
	if _, err := c.Login(ctx, user, "not the password"); err == nil {
		t.Fatal("a wrong password signed in")
	}
	before, err := c.Login(ctx, user, pass)
	if err != nil {
		t.Fatal(err)
	}
	c.Add(1000, 5)
	after, err := c.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.UsedBytes != before.UsedBytes+1000 || after.UsedSeconds != before.UsedSeconds+5 {
		t.Fatalf("before %+v, after %+v", before, after)
	}
	t.Logf("account %+v", after)
}
