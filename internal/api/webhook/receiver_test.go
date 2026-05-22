package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestVerifyHMACSHA256_PrefixAndBareHex(t *testing.T) {
	body := []byte(`{"ok":true}`)
	secret := "shh"
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	if !VerifyHMACSHA256(secret, "sha256="+sig, body, "sha256=") {
		t.Fatalf("prefixed GitHub-style signature should verify")
	}
	if !VerifyHMACSHA256(secret, sig, body, "") {
		t.Fatalf("bare Linear-style signature should verify")
	}
	if VerifyHMACSHA256(secret, "sha256="+sig, body, "") {
		t.Fatalf("bare verification must not accept a prefixed signature")
	}
	if VerifyHMACSHA256(secret, sig, []byte(`{"ok":false}`), "") {
		t.Fatalf("tampered body should not verify")
	}
}
