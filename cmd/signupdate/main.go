// signupdate signs the update manifest for the release workflow and prints
// the public key for the build, with the key derived from TOKEN_SECRET
// (internal/updatesig).
//
//	signupdate pubkey            prints the hex public key
//	signupdate sign update.json  adds "signed" and "sig" to the manifest
//	signupdate verify update.json checks the manifest against the key
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/Albatrossvpn/vp/internal/updatesig"
)

func main() {
	if len(os.Args) < 2 {
		fail("usage: signupdate pubkey | sign <file> | verify <file>")
	}
	secret := os.Getenv("TOKEN_SECRET")
	if len(secret) < 32 {
		fail("TOKEN_SECRET is not set (32 characters or more)")
	}
	priv := updatesig.KeyFromSecret(secret)
	switch os.Args[1] {
	case "pubkey":
		fmt.Println(updatesig.PublicHex(priv))
	case "sign", "verify":
		if len(os.Args) < 3 {
			fail("a file is needed")
		}
		path := os.Args[2]
		b, err := os.ReadFile(path)
		if err != nil {
			fail(err.Error())
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			fail("not a JSON manifest: " + err.Error())
		}
		if os.Args[1] == "verify" {
			signed, _ := m["signed"].(string)
			sig, _ := m["sig"].(string)
			payload, err := updatesig.Verify(updatesig.PublicHex(priv), signed, sig)
			if err != nil {
				fail("the manifest is not signed by this key: " + err.Error())
			}
			fmt.Printf("signed payload of %d bytes verifies\n", len(payload))
			return
		}
		delete(m, "signed")
		delete(m, "sig")
		payload, err := json.Marshal(m)
		if err != nil {
			fail(err.Error())
		}
		m["signed"], m["sig"] = updatesig.Sign(priv, payload)
		var out bytes.Buffer
		enc := json.NewEncoder(&out)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(m); err != nil {
			fail(err.Error())
		}
		if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
			fail(err.Error())
		}
		fmt.Fprintf(os.Stderr, "signed %s with key %s\n", path, updatesig.PublicHex(priv))
	default:
		fail("unknown command " + os.Args[1])
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
