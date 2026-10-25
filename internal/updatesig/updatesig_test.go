package updatesig

import (
	"strings"
	"testing"
)

func TestSignVerify(t *testing.T) {
	priv := KeyFromSecret(strings.Repeat("s", 32))
	if PublicHex(priv) != PublicHex(KeyFromSecret(strings.Repeat("s", 32))) {
		t.Fatal("the key must follow from the secret alone")
	}
	signed, sig := Sign(priv, []byte(`{"latest_version":"1.0.0"}`))
	got, err := Verify(PublicHex(priv), signed, sig)
	if err != nil || string(got) != `{"latest_version":"1.0.0"}` {
		t.Fatalf("verify: %v %q", err, got)
	}
	if _, err := Verify(PublicHex(KeyFromSecret("other")), signed, sig); err == nil {
		t.Fatal("another key must not verify")
	}
	if _, err := Verify(PublicHex(priv), signed, sig[:10]+"A"+sig[11:]); err == nil {
		t.Fatal("a changed signature must not verify")
	}
	if _, err := Verify("zz", signed, sig); err == nil {
		t.Fatal("a bad key must be refused")
	}
}
