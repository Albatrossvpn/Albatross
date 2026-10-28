package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"time"

	"github.com/Albatrossvpn/vp/internal/sources"
)

// The lists are sealed with a key per collection (sources.SealKeyed). The
// keys handed out so far are kept, newest first, for as long as a list
// sealed with them may still be served from a cache, so the account
// service can give every signed-in app the key its copy needs. The lists
// of the project's own servers get a key of their own (premium), which the
// service gives only to the plans that have the own servers.

// keyKeep is how many keys stay in the file (a day of collections, two
// keys each).
const keyKeep = 192

// listKeyFile is how the keys are kept and handed to the account service:
// the key in hex, the id first.
type listKeyFile struct {
	ID      uint64 `json:"id"`
	Key     string `json:"key"`
	Premium bool   `json:"premium,omitempty"`
}

// newListKey makes this run's keys, for the lists and for the own servers'
// lists, and returns them with the recent ones read from path, newest
// first. Their ids are the run's time, the premium one first: the newest
// key is always a list key, which the tester inside the country seals the
// hot.txt it publishes with (writeHot).
func newListKey(path string, now time.Time) (list, premium sources.ListKey, keyFile []listKeyFile) {
	var keys []listKeyFile
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			if json.Unmarshal(b, &keys) != nil {
				log.Printf("list keys: %s is not readable; starting afresh", path)
				keys = nil
			}
		}
	}
	id := uint64(now.Unix())
	for len(keys) > 0 && keys[0].ID >= id {
		id = keys[0].ID + 1 // two runs within a second still differ
	}
	premium, list = sources.ListKey{ID: id, Key: newKey(), Premium: true}, sources.ListKey{ID: id + 1, Key: newKey()}
	keys = append([]listKeyFile{
		{ID: list.ID, Key: hex.EncodeToString(list.Key)},
		{ID: premium.ID, Key: hex.EncodeToString(premium.Key), Premium: true},
	}, keys...)
	sort.SliceStable(keys, func(i, j int) bool { return keys[i].ID > keys[j].ID })
	if len(keys) > keyKeep {
		keys = keys[:keyKeep]
	}
	return list, premium, keys
}

func newKey() []byte {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		log.Fatal(err)
	}
	return k
}

// readListKeys reads keys in the form writeListKeys writes them, or as the
// account service hands them out ({"keys": [...]}), newest first. A missing
// or unreadable file gives none.
func readListKeys(path string) []sources.ListKey {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var files []listKeyFile
	if json.Unmarshal(b, &files) != nil {
		var wrapped struct {
			Keys []listKeyFile `json:"keys"`
		}
		if json.Unmarshal(b, &wrapped) != nil {
			log.Printf("list keys: %s is not readable", path)
			return nil
		}
		files = wrapped.Keys
	}
	var out []sources.ListKey
	for _, f := range files {
		if k, err := hex.DecodeString(f.Key); err == nil && len(k) == 32 {
			out = append(out, sources.ListKey{ID: f.ID, Key: k, Premium: f.Premium})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// writeListKeys writes the keys for the account service.
func writeListKeys(path string, keys []listKeyFile) error {
	b, err := json.MarshalIndent(keys, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// openList returns the sealed list (sources.SealKeyed) in the file at path
// opened with keys. The build bundles the collector's list in the app this
// way (build/snapshot.sh), so a first start has servers before anyone
// signs in.
func openList(path string, keys []sources.ListKey) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	id, ok := sources.KeyedID(string(b))
	if !ok {
		return "", fmt.Errorf("%s is not a sealed list", path)
	}
	text := sources.OpenKeyed(keys, string(b))
	if text == "" {
		return "", fmt.Errorf("no list key opens %s (its key is %d)", path, id)
	}
	return text, nil
}
