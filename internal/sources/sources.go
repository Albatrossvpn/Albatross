// Package sources knows where free servers are published and downloads
// them through whatever path currently works.
package sources

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// Kind says how a source is fetched and parsed.
type Kind string

const (
	KindSubscription Kind = "sub"      // a URL serving links, base64 or Clash YAML
	KindTelegram     Kind = "telegram" // a public channel read via t.me/s/
	KindVPNGate      Kind = "vpngate"  // the VPN Gate OpenVPN server list
)

// Tier orders sources by how often they are refreshed.
const (
	TierPriority = 0 // tested or Iran-focused lists, refreshed most often
	TierDeep     = 1 // everything else needed for full coverage
	TierUser     = 2 // added by the user
)

// Source is one place servers are published.
type Source struct {
	ID      string `json:"id"`
	Kind    Kind   `json:"kind"`
	URL     string `json:"url"` // channel name for KindTelegram
	Tier    int    `json:"tier"`
	Builtin bool   `json:"builtin"`
	Enabled bool   `json:"enabled"`
	// Central marks the lists the collector publishes (see cmd/collect):
	// every other built-in source, already gathered and deduplicated.
	Central bool `json:"central,omitempty"`
	// Sealed lists are encrypted with OwnKey (see Open).
	Sealed bool `json:"sealed,omitempty"`
}

// Label is a short human-readable name.
func (s Source) Label() string {
	switch s.Kind {
	case KindTelegram:
		return "@" + s.URL
	case KindVPNGate:
		return "VPN Gate"
	}
	u := strings.TrimPrefix(strings.TrimPrefix(s.URL, "https://"), "http://")
	u = strings.TrimPrefix(u, "raw.githubusercontent.com/")
	return u
}

//go:embed lists/priority.txt
var priorityList string

//go:embed lists/deep.txt
var deepList string

//go:embed lists/telegram.txt
var telegramList string

//go:embed lists/telegram-extra.txt
var telegramExtraList string

// snapshotGz is a copy of the collector's fresh.txt taken when the app
// was built (CI replaces the empty placeholder), so a first start under
// heavy filtering has servers to test before any list can be downloaded.
//
//go:embed lists/snapshot.txt.gz
var snapshotGz []byte

// Snapshot returns the servers bundled with the app, or "".
func Snapshot() string {
	zr, err := gzip.NewReader(bytes.NewReader(snapshotGz))
	if err != nil {
		return ""
	}
	b, err := io.ReadAll(io.LimitReader(zr, maxBodySize))
	if err != nil {
		return ""
	}
	return string(b)
}

// CentralBase is where the collector publishes its lists: a branch of the
// app's own repository, so the GitHub CDN mirrors work for it too.
const CentralBase = "https://raw.githubusercontent.com/cezomagoanlace29861-jpg/vp/servers/"

// CentralShards is how many all-N.txt files the collector writes.
const CentralShards = 8

// OwnBuckets is how many parts the collector splits the own servers into.
const OwnBuckets = 4

// Central returns the collector's lists: fresh.txt first, then the shards.
// install is this installation's random number, which picks the parts of
// the own servers it reads.
func Central(install uint32) []Source {
	var out []Source
	add := func(name string, tier int) {
		u := CentralBase + name
		out = append(out, Source{ID: ID(KindSubscription, u), Kind: KindSubscription, URL: u, Tier: tier, Builtin: true, Enabled: true, Central: true})
	}
	if ownKey() != nil {
		// Each install reads two of the OwnBuckets parts of the project's
		// own servers, so whoever unpacks one app learns only those, and
		// which part gets blocked shows which part leaked.
		a := install % OwnBuckets
		b := (a + 1 + (install/OwnBuckets)%(OwnBuckets-1)) % OwnBuckets
		for _, k := range []uint32{a, b} {
			add(fmt.Sprintf("own-%d.bin", k+1), TierPriority)
			out[len(out)-1].Sealed = true
		}
	}
	add("fresh.txt", TierPriority)
	for i := 1; i <= CentralShards; i++ {
		add(fmt.Sprintf("all-%d.txt", i), TierDeep)
	}
	return out
}

// OwnKey (hex, 32 bytes) decrypts own.bin, the list of the project's own
// servers (deploy/server). It is set at build time; without it the list is
// not read. It only keeps public collectors from picking those servers up
// and overloading them; it is not a secret from someone who unpacks the app.
var OwnKey string

func ownKey() []byte {
	k, err := hex.DecodeString(OwnKey)
	if err != nil || len(k) != 32 {
		return nil
	}
	return k
}

// Seal encrypts a list with key (AES-256-GCM, nonce first).
func Seal(key []byte, text string) ([]byte, error) {
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, []byte(text), nil), nil
}

// Open decrypts what Seal made with OwnKey, or returns "".
func Open(data string) string { return OpenWith(ownKey(), data) }

// ShareKey seals the server lists people pass to each other (see
// engine.ShareCode). Every build of the app has it, so it only keeps the
// links out of sight and catches a damaged or edited code; it does not
// stop someone who unpacks the app.
func ShareKey() []byte {
	h := sha256.Sum256(append([]byte("albatross share v1:"), ownKey()...))
	return h[:]
}

// OpenWith decrypts what Seal made with key, or returns "".
func OpenWith(key []byte, data string) string {
	g, err := gcm(key)
	if err != nil || len(data) < g.NonceSize() {
		return ""
	}
	b, err := g.Open(nil, []byte(data[:g.NonceSize()]), []byte(data[g.NonceSize():]), nil)
	if err != nil {
		return ""
	}
	return string(b)
}

func gcm(key []byte) (cipher.AEAD, error) {
	c, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(c)
}

// Collected returns every source the collector reads: the built-in ones
// and Telegram channels too many for each app to read itself.
func Collected() []Source {
	out := Builtin()
	for _, c := range lines(telegramExtraList) {
		out = append(out, Source{ID: ID(KindTelegram, c), Kind: KindTelegram, URL: c, Tier: TierPriority, Builtin: true, Enabled: true})
	}
	return out
}

// ID derives a stable identifier.
func ID(kind Kind, url string) string {
	h := sha1.Sum([]byte(string(kind) + "|" + url))
	return hex.EncodeToString(h[:6])
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		out = append(out, l)
	}
	return out
}

// Builtin returns every source shipped with the app.
func Builtin() []Source {
	var out []Source
	add := func(kind Kind, url string, tier int) {
		out = append(out, Source{ID: ID(kind, url), Kind: kind, URL: url, Tier: tier, Builtin: true, Enabled: true})
	}
	add(KindVPNGate, vpnGateURL, TierPriority)
	for _, u := range lines(priorityList) {
		add(KindSubscription, u, TierPriority)
	}
	for _, c := range lines(telegramList) {
		add(KindTelegram, c, TierPriority)
	}
	for _, u := range lines(deepList) {
		add(KindSubscription, u, TierDeep)
	}
	return out
}

// User makes a source from something the user pasted: a URL or a
// Telegram channel (t.me/name or @name).
func User(input string) (Source, bool) {
	input = strings.TrimSpace(input)
	if input == "" {
		return Source{}, false
	}
	lower := strings.ToLower(input)
	for _, p := range []string{"https://t.me/s/", "https://t.me/", "http://t.me/", "t.me/s/", "t.me/", "@"} {
		if strings.HasPrefix(lower, p) {
			name := strings.Trim(input[len(p):], "/ ")
			if i := strings.IndexAny(name, "/?#"); i >= 0 {
				name = name[:i]
			}
			if name == "" {
				return Source{}, false
			}
			return Source{ID: ID(KindTelegram, strings.ToLower(name)), Kind: KindTelegram, URL: strings.ToLower(name), Tier: TierUser, Enabled: true}, true
		}
	}
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return Source{ID: ID(KindSubscription, input), Kind: KindSubscription, URL: input, Tier: TierUser, Enabled: true}, true
	}
	return Source{}, false
}
