package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Each run adds a key with a new id and keeps the recent ones, newest first.
func TestListKeysRotate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")
	now := time.Unix(1761480000, 0)
	k1, keys := newListKey(path, now)
	if k1.ID != 1761480000 || len(keys) != 1 || len(k1.Key) != 32 {
		t.Fatalf("first key %+v %v", k1, keys)
	}
	if err := writeListKeys(path, keys); err != nil {
		t.Fatal(err)
	}
	// The same second again: a different id all the same, and the old key kept.
	k2, keys := newListKey(path, now)
	if k2.ID != 1761480001 || len(keys) != 2 || keys[0].ID != k2.ID || keys[1].ID != k1.ID {
		t.Fatalf("second key %+v %v", k2, keys)
	}
	// The file holds at most keyKeep keys.
	var many []listKeyFile
	for i := range keyKeep + 10 {
		many = append(many, listKeyFile{ID: uint64(1000 + i), Key: "00"})
	}
	b, _ := json.Marshal(many)
	os.WriteFile(path, b, 0o600)
	_, keys = newListKey(path, now)
	if len(keys) != keyKeep || keys[0].ID != 1761480000 {
		t.Fatalf("kept %d, first %d", len(keys), keys[0].ID)
	}
}
