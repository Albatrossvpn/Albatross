package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"sort"
	"time"

	"github.com/Albatrossvpn/vp/internal/sources"
)

// The lists are sealed with a key per collection (sources.SealKeyed). The
// keys handed out so far are kept, newest first, for as long as a list
// sealed with them may still be served from a cache, so the account
// service can give every signed-in app the key its copy needs.

// keyKeep is how many keys stay in the file (a day of collections).
const keyKeep = 96

// listKeyFile is how the keys are kept and handed to the account service:
// the key in hex, the id first.
type listKeyFile struct {
	ID  uint64 `json:"id"`
	Key string `json:"key"`
}

// newListKey makes this run's key (its id the run's time) and returns it
// with the recent ones read from path, newest first.
func newListKey(path string, now time.Time) (sources.ListKey, []listKeyFile) {
	var keys []listKeyFile
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			if json.Unmarshal(b, &keys) != nil {
				log.Printf("list keys: %s is not readable; starting afresh", path)
				keys = nil
			}
		}
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		log.Fatal(err)
	}
	id := uint64(now.Unix())
	for len(keys) > 0 && keys[0].ID >= id {
		id = keys[0].ID + 1 // two runs within a second still differ
	}
	fresh := sources.ListKey{ID: id, Key: k}
	keys = append([]listKeyFile{{ID: id, Key: hex.EncodeToString(k)}}, keys...)
	sort.SliceStable(keys, func(i, j int) bool { return keys[i].ID > keys[j].ID })
	if len(keys) > keyKeep {
		keys = keys[:keyKeep]
	}
	return fresh, keys
}

// writeListKeys writes the keys for the account service.
func writeListKeys(path string, keys []listKeyFile) error {
	b, err := json.MarshalIndent(keys, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}
