package engine

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Albatrossvpn/vp/internal/account"
	"github.com/Albatrossvpn/vp/internal/sources"
)

// The collector seals its lists with a key that changes every collection
// (sources.SealKeyed); the account service hands the recent keys to
// signed-in users (account.Client.ListKeys). A copy of the app with the
// sign-in cut out downloads the lists like anyone, and reads nothing. The
// keys are kept on disk so the cached lists open offline.

const (
	listKeysWait  = 25 * time.Second
	listKeysRetry = 2 * time.Minute // between attempts after a failure
)

var (
	listKeysMu      sync.Mutex
	listKeys        []sources.ListKey
	listKeysFetched time.Time // the last attempt, successful or not
	listKeysDir     string
)

func listKeysPath(dir string) string { return filepath.Join(dir, "listkeys.json") }

// loadListKeys reads the keys kept from the last answer.
func (e *Engine) loadListKeys() {
	listKeysMu.Lock()
	defer listKeysMu.Unlock()
	listKeysDir = e.dir
	b, err := os.ReadFile(listKeysPath(e.dir))
	if err != nil {
		return
	}
	var keys []sources.ListKey
	if json.Unmarshal(b, &keys) == nil {
		listKeys = keys
	}
}

// haveListKey says whether the key with id is known.
func haveListKey(id uint64) bool {
	listKeysMu.Lock()
	defer listKeysMu.Unlock()
	for _, k := range listKeys {
		if k.ID == id {
			return true
		}
	}
	return false
}

// refreshListKeys asks the service for the current keys: through the VPN
// when it is up, else through servers before going direct (tunnelFirst),
// as signing in does. Not more than once per listKeysRetry when it fails.
func (e *Engine) refreshListKeys() {
	if e.acct == nil || !e.acct.View().SignedIn {
		return
	}
	listKeysMu.Lock()
	soon := time.Since(listKeysFetched) < listKeysRetry
	if !soon {
		listKeysFetched = time.Now()
	}
	listKeysMu.Unlock()
	if soon {
		return
	}
	ctx, cancel := context.WithTimeout(e.ctx, listKeysWait+listKeysWait)
	defer cancel()
	var got []account.ListKey
	err := e.tunnelFirst(ctx, listKeysWait, func(ctx context.Context) error {
		var err error
		got, err = e.acct.ListKeys(ctx)
		return err
	})
	switch {
	case err == nil:
		keys := make([]sources.ListKey, 0, len(got))
		for _, k := range got {
			if b, err := hex.DecodeString(k.Key); err == nil && len(b) == 32 {
				keys = append(keys, sources.ListKey{ID: k.ID, Key: b})
			}
		}
		listKeysMu.Lock()
		listKeys = keys
		listKeysMu.Unlock()
		if b, err := json.Marshal(keys); err == nil {
			os.WriteFile(listKeysPath(e.dir), b, 0o600)
		}
		e.logf("info", "The lists' keys were refreshed (%d).", len(keys))
	case errors.Is(err, account.ErrSignedOut), e.ctx.Err() != nil:
	default:
		e.logf("warn", "The lists' keys could not be fetched: %v", err)
	}
}

// openKeyed opens a sealed list, fetching the keys when the list's key is
// not known yet. "" when it cannot be opened.
func (e *Engine) openKeyed(text string) string {
	id, ok := sources.KeyedID(text)
	if !ok {
		return ""
	}
	if !haveListKey(id) {
		e.refreshListKeys()
	}
	listKeysMu.Lock()
	keys := listKeys
	listKeysMu.Unlock()
	return sources.OpenKeyed(keys, text)
}

// forgetListKeys drops the keys, on sign-out.
func forgetListKeys() {
	listKeysMu.Lock()
	defer listKeysMu.Unlock()
	listKeys, listKeysFetched = nil, time.Time{}
	if listKeysDir != "" {
		os.Remove(listKeysPath(listKeysDir))
	}
}
