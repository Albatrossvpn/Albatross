package engine

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cezomagoanlace29861-jpg/vp/internal/account"
)

// AccountURL is the account service, set at build time with
// -ldflags "-X .../internal/engine.AccountURL=https://a.example,https://b.example".
// Several addresses may be given, comma separated; they are tried in order,
// which helps where one of them is blocked. Empty means no accounts: the app
// connects without signing in (development builds and tests).
var AccountURL = ""

// ErrLoginRequired means the user has to sign in before connecting.
var ErrLoginRequired = errors.New("Sign in to connect.")

// LimitError means the user's account may not connect right now.
type LimitError struct {
	Reason  string
	Message string
}

func (e *LimitError) Error() string { return e.Message }

const (
	// An answer from the service this recent is not asked for again when
	// connecting.
	syncFresh = 2 * time.Minute
	// How often use is added up and how often the service is asked.
	meterEvery = 15 * time.Second
	syncEvery  = time.Minute
	// How long connecting waits for the service.
	connectSync = 6 * time.Second
)

// AccountState is the signed-in user's account as the UI shows it.
type AccountState struct {
	Enabled     bool   `json:"enabled"` // this build asks users to sign in
	SignedIn    bool   `json:"signed_in"`
	Username    string `json:"username,omitempty"`
	Message     string `json:"message,omitempty"` // why the account cannot connect, if so
	QuotaBytes  int64  `json:"quota_bytes"`
	UsedBytes   int64  `json:"used_bytes"`
	MaxSeconds  int64  `json:"max_seconds"`
	UsedSeconds int64  `json:"used_seconds"`
	ExpiresAt   int64  `json:"expires_at"`
	ResetsAt    int64  `json:"resets_at"`
	SiteURL     string `json:"site_url,omitempty"`
}

func accountURLs(opt Options) []string {
	list := opt.AccountURLs
	if list == nil {
		list = strings.Split(AccountURL, ",")
	}
	var out []string
	for _, u := range list {
		if u = strings.TrimSpace(u); strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://127.0.0.1") {
			out = append(out, u)
		}
	}
	return out
}

// Account returns the account for the UI.
func (e *Engine) Account() AccountState {
	if e.acct == nil {
		return AccountState{}
	}
	st := AccountState{Enabled: true, SiteURL: e.acct.Site()}
	v := e.acct.View()
	if !v.SignedIn {
		return st
	}
	i := v.Info
	st.SignedIn, st.Username = true, i.Username
	st.QuotaBytes, st.UsedBytes, st.MaxSeconds, st.UsedSeconds = i.QuotaBytes, i.UsedBytes, i.MaxSeconds, i.UsedSeconds
	st.ExpiresAt, st.ResetsAt = i.ExpiresAt, i.ResetsAt
	if why := i.Why(time.Now()); why != "" {
		st.Message = limitMessage(why, i)
	}
	return st
}

// Login signs the user in to the account service.
func (e *Engine) Login(username, password string) error {
	if e.acct == nil {
		return errors.New("Accounts are not used in this build.")
	}
	if strings.TrimSpace(username) == "" || password == "" {
		return errors.New("Enter your username and password.")
	}
	if _, err := e.acct.Login(context.Background(), username, password); err != nil {
		if errors.Is(err, account.ErrUnreachable) {
			return errors.New("Cannot reach the account service. Check your internet connection and try again.")
		}
		return err
	}
	e.logf("info", "Signed in.")
	return nil
}

// Logout disconnects and forgets the session.
func (e *Engine) Logout() {
	if e.acct == nil {
		return
	}
	e.Disconnect()
	e.acct.Logout()
}

// AllowConnect says whether the signed-in user may connect now, asking the
// service unless it answered a moment ago. With no service reachable the
// last answer stands for a few days.
func (e *Engine) AllowConnect() error {
	if e.acct == nil {
		return nil
	}
	v := e.acct.View()
	if !v.SignedIn {
		return ErrLoginRequired
	}
	info, now := v.Info, e.acct.Now()
	if now.Sub(v.SyncedAt) > syncFresh || info.Why(now) != "" {
		ctx, cancel := context.WithTimeout(context.Background(), connectSync)
		got, err := e.acct.Sync(ctx)
		cancel()
		switch {
		case err == nil:
			info = got
		case errors.Is(err, account.ErrSignedOut):
			return ErrLoginRequired
		case v.Stale:
			return errors.New("Cannot reach the account service. Check your internet connection and try again.")
		}
	}
	if why := info.Why(now); why != "" {
		return &LimitError{Reason: why, Message: limitMessage(why, info)}
	}
	return nil
}

func limitMessage(reason string, i account.Info) string {
	renew := ""
	if i.ResetsAt > 0 {
		renew = " It renews on " + time.Unix(i.ResetsAt, 0).UTC().Format("2 Jan 2006") + "."
	}
	switch reason {
	case account.ReasonQuota:
		return "Your data allowance is used up." + renew
	case account.ReasonTime:
		return "Your time allowance is used up." + renew
	case account.ReasonExpired:
		return "Your account has expired."
	}
	return "Your account is turned off."
}

// meterState is what meterStep has already counted.
type meterState struct {
	bytes  int64
	at     time.Time // use is counted in whole seconds up to here
	synced time.Time
}

// meter adds up the connection's data and time for the account and asks the
// service whether the user may go on, until the connection ends.
func (e *Engine) meter(ctx context.Context, c *connection) {
	m := &meterState{at: time.Now(), synced: time.Now()}
	t := time.NewTicker(meterEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			e.meterStep(context.Background(), c, m, false)
			return
		case <-t.C:
			if e.meterStep(ctx, c, m, true) {
				return
			}
		}
	}
}

// meterStep counts use since the last step and, when the account is over a
// limit or the service is due to be asked, checks with the service. It
// disconnects an account that may not go on and reports whether it did.
func (e *Engine) meterStep(ctx context.Context, c *connection, m *meterState, check bool) bool {
	now := time.Now()
	c.mu.Lock()
	total := c.up + c.down
	c.mu.Unlock()
	secs := int64(now.Sub(m.at) / time.Second)
	e.acct.Add(total-m.bytes, secs)
	m.bytes = total
	m.at = m.at.Add(time.Duration(secs) * time.Second)
	if !check {
		return false
	}
	v := e.acct.View()
	why, info := v.Info.Why(now), v.Info
	if v.SignedIn && (why != "" || now.Sub(m.synced) >= syncEvery) {
		// A local count over a limit is confirmed with the service first:
		// the allowance may have renewed.
		got, err := e.acct.Sync(ctx)
		switch {
		case err == nil:
			info, why, m.synced = got, got.Why(now), now
		case errors.Is(err, account.ErrSignedOut):
			e.stopForAccount("Sign in to connect.")
			return true
		case ctx.Err() != nil:
			return false
		case v.Stale:
			e.stopForAccount("Cannot reach the account service. Check your internet connection and try again.")
			return true
		}
	}
	if !v.SignedIn {
		e.stopForAccount("Sign in to connect.")
		return true
	}
	if why != "" {
		e.logf("warn", "Account limit reached (%s).", why)
		e.stopForAccount(limitMessage(why, info))
		return true
	}
	return false
}

// stopForAccount disconnects and tells the user why.
func (e *Engine) stopForAccount(message string) {
	go func() {
		e.Disconnect()
		e.setStatus(StatusIdle, message)
	}()
}
