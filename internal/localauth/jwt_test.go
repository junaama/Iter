package localauth

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestDecodeClaimsReadsTenantSubjectExpiryAndDisplayName(t *testing.T) {
	token := testJWT(t, map[string]any{
		"sub":       "user_123",
		"tenant_id": "018f4a13-d40a-7420-9f8d-8437e8ad9d77",
		"exp":       float64(1_700_000_000),
		"email":     "priya@iter.dev",
	})

	claims, err := DecodeClaims(token)
	if err != nil {
		t.Fatalf("DecodeClaims: %v", err)
	}
	if claims.Subject != "user_123" {
		t.Fatalf("Subject: got %q", claims.Subject)
	}
	if claims.TenantID != "018f4a13-d40a-7420-9f8d-8437e8ad9d77" {
		t.Fatalf("TenantID: got %q", claims.TenantID)
	}
	if !claims.ExpiresAt.Equal(time.Unix(1_700_000_000, 0)) {
		t.Fatalf("ExpiresAt: got %s", claims.ExpiresAt)
	}
	if claims.DisplayName != "priya@iter.dev" {
		t.Fatalf("DisplayName: got %q", claims.DisplayName)
	}
}

func testJWT(t *testing.T, payload map[string]any) string {
	t.Helper()
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(encodedPayload) + "."
}
