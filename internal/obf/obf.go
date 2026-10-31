// Package obf hides a build-time secret (the domestic mirror's address) in
// the shipped binary. A value injected with -ldflags -X is written into the
// binary as it is, so `strings` on the app would show it; garble does not
// touch such values either. So the build encodes the secret with Hide and
// injects the encoded form, and the app decodes it at runtime with Reveal.
//
// This is not strong cryptography: a determined reverse-engineer who reads
// the (garble-obfuscated) decode and its key can recover it. It only keeps
// the address out of a plain `strings` scan, which is what matters: the
// domestic mirrors must not be readable at a glance from the app.
package obf

import (
	"encoding/base64"
	"strings"
)

// mark begins an encoded value, so Reveal leaves a plain one (a development
// build, or a value that was never hidden) untouched.
const mark = "obf:v1:"

// key is XORed over the bytes, repeating. garble -literals obfuscates it in
// the shipped binary, so it is not a plain constant there. cmd/obf uses the
// same key to encode, so the two always agree.
const key = "albatross-mirror-veil-7fQ2xL9"

// Hide encodes s (the build uses it, through cmd/obf); "" stays "".
func Hide(s string) string {
	if s == "" {
		return ""
	}
	return mark + base64.RawStdEncoding.EncodeToString(xor([]byte(s)))
}

// Reveal decodes what Hide made; a value without the mark is returned as it
// is, so plain builds and already-plain values keep working.
func Reveal(s string) string {
	rest, ok := strings.CutPrefix(s, mark)
	if !ok {
		return s
	}
	b, err := base64.RawStdEncoding.DecodeString(rest)
	if err != nil {
		return ""
	}
	return string(xor(b))
}

func xor(b []byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[i] = b[i] ^ key[i%len(key)]
	}
	return out
}
