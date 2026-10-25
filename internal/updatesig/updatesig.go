// Package updatesig signs and checks the update manifest. The key pair is
// derived from the account service's TOKEN_SECRET, so the release workflow
// signs and the build learns the public key without another secret; an
// attacker who can serve a forged manifest (a CDN mirror, a taken-over
// account) cannot make installed copies download or run anything.
package updatesig

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
)

// KeyFromSecret derives the signing key from the shared secret.
func KeyFromSecret(secret string) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("albatross-update-key:" + secret))
	return ed25519.NewKeyFromSeed(seed[:])
}

// PublicHex is the public key as the build passes it to the app.
func PublicHex(priv ed25519.PrivateKey) string {
	return hex.EncodeToString(priv.Public().(ed25519.PublicKey))
}

// Sign returns the manifest payload and its signature as the manifest
// carries them: the payload is the exact bytes signed, base64, so the
// verifier needs no canonical form.
func Sign(priv ed25519.PrivateKey, payload []byte) (signed, sig string) {
	return base64.StdEncoding.EncodeToString(payload), base64.StdEncoding.EncodeToString(ed25519.Sign(priv, payload))
}

// Verify checks signed and sig against the hex public key and returns the
// payload bytes.
func Verify(pubHex, signed, sig string) ([]byte, error) {
	pub, err := hex.DecodeString(pubHex)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("bad public key")
	}
	payload, err := base64.StdEncoding.DecodeString(signed)
	if err != nil {
		return nil, errors.New("bad payload")
	}
	s, err := base64.StdEncoding.DecodeString(sig)
	if err != nil || len(s) != ed25519.SignatureSize {
		return nil, errors.New("bad signature")
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), payload, s) {
		return nil, errors.New("signature does not verify")
	}
	return payload, nil
}
