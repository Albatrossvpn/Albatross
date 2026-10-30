package node

import (
	"crypto/sha1"
	"fmt"
	"regexp"
	"strings"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isUUID(s string) bool { return uuidRe.MatchString(s) }

var hex32Re = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// dashUUID writes a UUID given as 32 hex digits, with or without stray
// dashes, in its usual form.
func dashUUID(s string) (string, bool) {
	h := strings.ReplaceAll(s, "-", "")
	if !hex32Re.MatchString(h) {
		return "", false
	}
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], true
}

// uuidV5 maps a non-UUID id to a UUID the same way Xray does:
// UUIDv5 with the all-zero namespace.
func uuidV5(name string) string {
	h := sha1.New()
	h.Write(make([]byte, 16))
	h.Write([]byte(name))
	b := h.Sum(nil)[:16]
	b[6] = (b[6] & 0x0f) | 0x50
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
