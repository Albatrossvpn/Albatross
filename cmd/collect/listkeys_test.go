package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Albatrossvpn/vp/internal/sources"
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

// The keys are read in both forms: the collector's file and the account
// service's answer; bad keys are dropped and the newest comes first.
func TestReadListKeys(t *testing.T) {
	dir := t.TempDir()
	key := strings.Repeat("ab", 32)
	plain := filepath.Join(dir, "keys.json")
	os.WriteFile(plain, []byte(`[{"id":5,"key":"`+key+`"},{"id":9,"key":"`+key+`"},{"id":7,"key":"00"}]`), 0o600)
	wrapped := filepath.Join(dir, "api.json")
	os.WriteFile(wrapped, []byte(`{"keys":[{"id":3,"key":"`+key+`"}],"reason":""}`), 0o600)
	if got := readListKeys(plain); len(got) != 2 || got[0].ID != 9 || got[1].ID != 5 || len(got[0].Key) != 32 {
		t.Fatalf("plain: %+v", got)
	}
	if got := readListKeys(wrapped); len(got) != 1 || got[0].ID != 3 {
		t.Fatalf("wrapped: %+v", got)
	}
	if got := readListKeys(filepath.Join(dir, "missing.json")); got != nil {
		t.Fatalf("missing: %+v", got)
	}
}

// refreshHot reads the sealed lists the collector publishes, and what it
// writes the apps can open with the newest key.
func TestHotFromSealedLists(t *testing.T) {
	dir := t.TempDir()
	keys := []sources.ListKey{{ID: 20, Key: bytes.Repeat([]byte{2}, 32)}, {ID: 10, Key: bytes.Repeat([]byte{1}, 32)}}
	a := "vless://11111111-1111-1111-1111-111111111111@192.0.2.1:443?security=tls#a"
	b := "vless://22222222-2222-2222-2222-222222222222@192.0.2.2:443?security=tls#b"
	c := "trojan://pw@192.0.2.3:443#c"
	hot, _ := sources.SealKeyed(keys[1], a+"\n"+b+"\n")
	fresh, _ := sources.SealKeyed(keys[0], b+"\n"+c+"\n")
	other, _ := sources.SealKeyed(sources.ListKey{ID: 99, Key: bytes.Repeat([]byte{9}, 32)}, a+"\n")
	for name, data := range map[string][]byte{"hot.txt.sealed": hot, "fresh.txt.sealed": fresh, "other.sealed": other, "plain.txt": []byte(c + "\n")} {
		os.WriteFile(filepath.Join(dir, name), data, 0o644)
	}
	paths := []string{filepath.Join(dir, "hot.txt.sealed"), filepath.Join(dir, "other.sealed"), filepath.Join(dir, "missing"), filepath.Join(dir, "fresh.txt.sealed"), filepath.Join(dir, "plain.txt")}
	got := links(hotInput(paths, keys))
	if strings.Join(got, " ") != strings.Join([]string{a, b, c}, " ") {
		t.Fatalf("candidates %q", got)
	}

	out := t.TempDir()
	if err := writeHot(out, []string{a, c}, keys, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "hot.txt")); err == nil {
		t.Fatal("a plain hot.txt was written without -plain")
	}
	data, _ := os.ReadFile(filepath.Join(out, "hot.txt.sealed"))
	if id, _ := sources.KeyedID(string(data)); id != 20 || sources.OpenKeyed(keys, string(data)) != a+"\n"+c+"\n" {
		t.Fatalf("sealed hot.txt: id %d", id)
	}
	// Without keys (an old setup), the plain list as before.
	if err := writeHot(out, []string{a}, nil, false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "hot.txt")); string(b) != a+"\n" {
		t.Fatalf("plain hot.txt %q", b)
	}
}

// The build opens the newest lists with the keys to bundle them in the app;
// a list without its key, or one that is not sealed, is refused.
func TestOpenList(t *testing.T) {
	dir := t.TempDir()
	keys := []sources.ListKey{{ID: 21, Key: bytes.Repeat([]byte{2}, 32)}, {ID: 20, Key: bytes.Repeat([]byte{1}, 32)}}
	text := "trojan://pw@192.0.2.3:443#c\n"
	sealed, _ := sources.SealKeyed(keys[1], text)
	other, _ := sources.SealKeyed(sources.ListKey{ID: 99, Key: bytes.Repeat([]byte{9}, 32)}, text)
	for name, data := range map[string][]byte{"fresh.txt.sealed": sealed, "other.sealed": other, "fresh.txt": []byte(text)} {
		os.WriteFile(filepath.Join(dir, name), data, 0o644)
	}
	if got, err := openList(filepath.Join(dir, "fresh.txt.sealed"), keys); err != nil || got != text {
		t.Fatalf("sealed list: %q, %v", got, err)
	}
	for _, name := range []string{"other.sealed", "fresh.txt", "missing"} {
		if got, err := openList(filepath.Join(dir, name), keys); err == nil {
			t.Fatalf("%s opened: %q", name, got)
		}
	}
}
