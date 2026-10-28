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
	"encoding/binary"
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
	KindLEAP         Kind = "leap"     // a LEAP provider (Riseup): OpenVPN with an anonymous certificate
	KindFeed         Kind = "feed"     // an RSS or Atom feed (a channel's videos): the lists its newest entries link to
	KindWarp         Kind = "warp"     // the device's own WARP account: servers made on the device (Fetcher.Warp)
)

// Warp is the source of the servers made from the device's own free WARP
// account: AmneziaWG and WireGuard to WARP's addresses, which give the
// protocols that have next to no public servers some of their own. Only
// the apps have it (the collector has no device), and it is not one of the
// built-in lists, which the collector's lists stand in for.
func Warp() Source {
	return Source{ID: ID(KindWarp, "device"), Kind: KindWarp, URL: "device", Tier: TierPriority, Enabled: true}
}

// leapProviders are the LEAP providers the app knows, by API address: the
// address of their CA certificate (provider.json's ca_cert_uri). Calyx was
// one: its API name (api.calyx.net, in the provider file of its last
// release, 2023) no longer has an address, calyx.net:4430 does not answer,
// and its CA address answers 404.
var leapProviders = map[string]string{
	"https://api.black.riseup.net": "https://black.riseup.net/ca.crt",
}

// LEAPName is the provider's short name ("riseup") for an API address.
func LEAPName(api string) string {
	h := strings.TrimPrefix(api, "https://")
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	parts := strings.Split(h, ".")
	if len(parts) >= 2 {
		return parts[len(parts)-2]
	}
	return h
}

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
	// Hot marks the collector's hot.txt; its servers are tested first.
	Hot bool `json:"hot,omitempty"`
	// Delta marks the collector's new.txt, the latest additions.
	Delta bool `json:"delta,omitempty"`
	// Sealed lists are encrypted with OwnKey (see Open).
	Sealed bool `json:"sealed,omitempty"`
	// Keyed lists are sealed with the list key of the collection that
	// wrote them (SealKeyed), which the account service hands to signed-in
	// users only (see OpenKeyed).
	Keyed bool `json:"keyed,omitempty"`
	// Relay marks the sealed list of the relays inside the country (own-0.bin).
	Relay bool `json:"relay,omitempty"`
}

// Label is a short human-readable name.
func (s Source) Label() string {
	switch s.Kind {
	case KindTelegram:
		return "@" + s.URL
	case KindVPNGate:
		return "VPN Gate"
	case KindWarp:
		return "Cloudflare WARP (this device)"
	case KindLEAP:
		n := LEAPName(s.URL)
		return strings.ToUpper(n[:1]) + n[1:] + " VPN"
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

//go:embed lists/extra.txt
var extraList string

//go:embed lists/feeds.txt
var feedList string

// snapshotGz is a copy of the collector's fresh.txt taken when the app
// was built (CI replaces the empty placeholder), so a first start under
// heavy filtering has servers to test before any list can be downloaded.
//
//go:embed lists/snapshot.txt.gz
var snapshotGz []byte

// ownSnapshot is the sealed relay list (own-0.bin) as it was when the app
// was built, so a fresh install during a shutdown knows the in-country
// relays. Sealed bytes are safe to carry.
//
//go:embed lists/own-0.bin
var ownSnapshot []byte

// SnapshotFor is what the app carries for a source when nothing was ever
// downloaded: the relay list for the relay source, the collector's list
// for the plain central ones, nothing for the other sealed buckets (a
// plaintext snapshot would not open).
func SnapshotFor(s Source) string {
	switch {
	case s.Relay:
		return string(ownSnapshot)
	case s.Sealed:
		return ""
	}
	return Snapshot()
}

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
const CentralBase = "https://raw.githubusercontent.com/Albatrossvpn/Albatross/servers/"

// CentralShards is how many all-N.txt files the collector writes.
const CentralShards = 8

// OwnBuckets is how many parts the collector splits the own servers into.
const OwnBuckets = 4

// Central returns the collector's lists: hot.txt and fresh.txt first, then
// the shards.
// install is this installation's random number, which picks the parts of
// the own servers it reads.
func Central(install uint32) []Source {
	var out []Source
	add := func(name string, tier int) {
		u := CentralBase + name
		out = append(out, Source{ID: ID(KindSubscription, u), Kind: KindSubscription, URL: u, Tier: tier, Builtin: true, Enabled: true, Central: true})
	}
	if ownKey() != nil {
		// own-0.bin holds the relays inside the country (deploy/server
		// relay mode), which every install must know: in a shutdown they
		// are the only way out.
		add("own-0.bin"+KeyedSuffix, TierPriority)
		out[len(out)-1].Sealed, out[len(out)-1].Keyed, out[len(out)-1].Relay = true, true, true
		// Each install reads two of the OwnBuckets parts of the project's
		// own servers, so whoever unpacks one app learns only those, and
		// which part gets blocked shows which part leaked.
		a := install % OwnBuckets
		b := (a + 1 + (install/OwnBuckets)%(OwnBuckets-1)) % OwnBuckets
		for _, k := range []uint32{a, b} {
			add(fmt.Sprintf("own-%d.bin", k+1)+KeyedSuffix, TierPriority)
			out[len(out)-1].Sealed, out[len(out)-1].Keyed = true, true
		}
	}
	// hot.txt is the few hundred servers that passed the collector's real
	// test most recently: small, re-tested every 20 minutes, tried first.
	// The lists go out sealed with a key that changes every collection
	// and that only signed-in users get from the account service, so a
	// copy of the app with the sign-in cut out reads nothing.
	add("hot.txt"+KeyedSuffix, TierPriority)
	out[len(out)-1].Hot, out[len(out)-1].Keyed = true, true
	// new.txt is what fresh.txt gained lately; phones fetch it often and
	// the whole fresh.txt rarely.
	add("new.txt"+KeyedSuffix, TierPriority)
	out[len(out)-1].Delta, out[len(out)-1].Keyed = true, true
	add("fresh.txt"+KeyedSuffix, TierPriority)
	out[len(out)-1].Keyed = true
	for i := 1; i <= CentralShards; i++ {
		add(fmt.Sprintf("all-%d.txt", i)+KeyedSuffix, TierDeep)
		out[len(out)-1].Keyed = true
	}
	return out
}

// KeyedSuffix ends the name of a list sealed with a list key.
const KeyedSuffix = ".sealed"

// keyedMagic opens a keyed list: the format, then the key's id.
const keyedMagic = "ALBK1"

// ListKey is one of the keys the lists are sealed with: its id (the time
// of the collection that made it) and the key itself.
type ListKey struct {
	ID  uint64 `json:"id"`
	Key []byte `json:"key"`
	// Premium keys seal the project's own servers' lists (the collector's
	// keys file says which); the account service gives them only to the
	// plans that have those servers.
	Premium bool `json:"premium,omitempty"`
}

// SealKeyed seals a list with a list key; the key's id travels in front so
// the app knows which key opens it.
func SealKeyed(k ListKey, text string) ([]byte, error) {
	sealed, err := Seal(k.Key, text)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(keyedMagic)+8+len(sealed))
	out = append(out, keyedMagic...)
	out = binary.BigEndian.AppendUint64(out, k.ID)
	return append(out, sealed...), nil
}

// KeyedID returns the id of the key a keyed list needs, or false when the
// data is not a keyed list.
func KeyedID(data string) (uint64, bool) {
	if len(data) < len(keyedMagic)+8 || data[:len(keyedMagic)] != keyedMagic {
		return 0, false
	}
	return binary.BigEndian.Uint64([]byte(data[len(keyedMagic) : len(keyedMagic)+8])), true
}

// OpenKeyed opens a keyed list with the key of its id among keys, or
// returns "".
func OpenKeyed(keys []ListKey, data string) string {
	id, ok := KeyedID(data)
	if !ok {
		return ""
	}
	for _, k := range keys {
		if k.ID == id {
			return OpenWith(k.Key, data[len(keyedMagic)+8:])
		}
	}
	return ""
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
// and the Telegram channels, lists, dated files and feeds too many for each
// app to read itself.
func Collected() []Source {
	out := Builtin()
	for _, c := range lines(telegramExtraList) {
		out = append(out, Source{ID: ID(KindTelegram, c), Kind: KindTelegram, URL: c, Tier: TierPriority, Builtin: true, Enabled: true})
	}
	for _, u := range lines(extraList) {
		out = append(out, Source{ID: ID(KindSubscription, u), Kind: KindSubscription, URL: u, Tier: TierDeep, Builtin: true, Enabled: true})
	}
	for _, u := range lines(feedList) {
		out = append(out, Source{ID: ID(KindFeed, u), Kind: KindFeed, URL: u, Tier: TierDeep, Builtin: true, Enabled: true})
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
	for _, api := range []string{"https://api.black.riseup.net"} {
		add(KindLEAP, api, TierPriority)
	}
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
