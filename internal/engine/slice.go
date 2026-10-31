package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Albatrossvpn/vp/internal/account"
	"github.com/Albatrossvpn/vp/internal/sources"
)

// No app is given the keys of the collector's lists of what it collected:
// the account service hands each signed-in account a slice of the servers
// it tested best instead, the same all day and another the next
// (account.Client.Servers). The app keeps the slice on disk, as it keeps
// the lists' keys, and reads it as the source sources.Account, so its
// servers are tested first like the hot list's were. Signing out deletes it.

const (
	sliceRefresh = 3 * time.Hour    // how often the service is asked for the slice
	sliceRetry   = 10 * time.Minute // ... after a failure
	sliceWait    = 25 * time.Second
)

// acctSlice is the slice as the engine holds it.
type acctSlice struct {
	mu      sync.Mutex
	links   []string
	fetched time.Time // the last attempt, successful or not; zero: due
}

func slicePath(dir string) string { return filepath.Join(dir, "servers.json") }

// loadSlice reads the slice kept from the last answer.
func (e *Engine) loadSlice() {
	e.acctSlice.mu.Lock()
	defer e.acctSlice.mu.Unlock()
	b, err := os.ReadFile(slicePath(e.dir))
	if err != nil {
		return
	}
	var links []string
	if json.Unmarshal(b, &links) == nil {
		e.acctSlice.links = links
	}
}

// sliceText is the slice as a list, for the source sources.Account: ""
// while signed out or before the first answer.
func (e *Engine) sliceText(context.Context) (string, error) {
	if e.acct == nil || !e.acct.View().SignedIn {
		return "", nil
	}
	e.acctSlice.mu.Lock()
	defer e.acctSlice.mu.Unlock()
	if len(e.acctSlice.links) == 0 {
		return "", nil
	}
	return strings.Join(e.acctSlice.links, "\n") + "\n", nil
}

// refreshSlice asks the service for the account's slice when due: through
// the VPN when it is up; otherwise only when no slice is kept yet, through
// servers before going direct (tunnelFirst), as signing in does. A new
// slice is read into the pool at once.
func (e *Engine) refreshSlice() {
	if e.acct == nil || !e.acct.View().SignedIn || e.ctx == nil {
		return
	}
	now := time.Now()
	connected := e.proxyClient() != nil
	e.acctSlice.mu.Lock()
	due := now.Sub(e.acctSlice.fetched) >= sliceRefresh
	have := len(e.acctSlice.links) > 0
	if due && (connected || !have) {
		e.acctSlice.fetched = now
	} else {
		due = false
	}
	e.acctSlice.mu.Unlock()
	if !due {
		return
	}
	ctx, cancel := context.WithTimeout(e.ctx, sliceWait+sliceWait)
	defer cancel()
	var got []string
	err := e.tunnelFirst(ctx, sliceWait, func(ctx context.Context) error {
		var err error
		got, err = e.acct.Servers(ctx)
		return err
	})
	switch {
	case err == nil:
		e.acctSlice.mu.Lock()
		changed := !slices.Equal(got, e.acctSlice.links)
		e.acctSlice.links = got
		e.acctSlice.mu.Unlock()
		if b, err := json.Marshal(got); err == nil {
			os.WriteFile(slicePath(e.dir), b, 0o600)
		}
		e.logf("info", "The account's servers were refreshed (%d).", len(got))
		if changed {
			e.rereadSlice()
		}
	case errors.Is(err, account.ErrSignedOut), e.ctx.Err() != nil:
	default:
		e.acctSlice.mu.Lock()
		e.acctSlice.fetched = now.Add(-sliceRefresh + sliceRetry)
		e.acctSlice.mu.Unlock()
		e.logf("warn", "The account's servers could not be fetched: %v", err)
	}
}

// rereadSlice makes the source sources.Account due, so the next refresh
// reads the new slice into the pool.
func (e *Engine) rereadSlice() {
	e.mu.Lock()
	for i, s := range e.srcs {
		if s.Kind == sources.KindAccount && i < len(e.srcStats) {
			e.srcStats[i].Fetched = time.Time{}
		}
	}
	e.mu.Unlock()
	e.triggerRefill()
}

// forgetSlice drops the slice, on sign-out: from memory, from disk, and
// the servers of it from those in use.
func (e *Engine) forgetSlice() {
	e.acctSlice.mu.Lock()
	e.acctSlice.links, e.acctSlice.fetched = nil, time.Time{}
	os.Remove(slicePath(e.dir))
	e.acctSlice.mu.Unlock()
	label := sources.Account().Label()
	e.mu.Lock()
	for k, s := range e.good {
		if s.Source == label {
			delete(e.good, k)
		}
	}
	for i, s := range e.srcs {
		if s.Kind == sources.KindAccount && i < len(e.srcStats) {
			e.srcStats[i].Fetched, e.srcStats[i].Nodes = time.Time{}, 0
		}
	}
	e.mu.Unlock()
}

// sliceNow asks for the account's slice at once when there is none yet,
// whatever the retry timer says.
func (e *Engine) sliceNow() {
	e.acctSlice.mu.Lock()
	empty := len(e.acctSlice.links) == 0
	if empty {
		e.acctSlice.fetched = time.Time{}
	}
	e.acctSlice.mu.Unlock()
	if empty {
		go e.refreshSlice()
	}
}
