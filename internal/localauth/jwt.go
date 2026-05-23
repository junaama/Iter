package localauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidJWT        = errors.New("invalid jwt")
	ErrMissingExpiration = errors.New("jwt missing exp")
	ErrMissingSubject    = errors.New("jwt missing sub")
)

type Claims struct {
	Subject     string
	TenantID    string
	ExpiresAt   time.Time
	DisplayName string
}

func DecodeClaims(token string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return Claims{}, ErrInvalidJWT
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, ErrInvalidJWT
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return Claims{}, ErrInvalidJWT
	}
	sub, _ := raw["sub"].(string)
	if sub == "" {
		return Claims{}, ErrMissingSubject
	}
	expFloat, ok := raw["exp"].(float64)
	if !ok {
		return Claims{}, ErrMissingExpiration
	}
	displayName, _ := raw["name"].(string)
	if displayName == "" {
		displayName, _ = raw["email"].(string)
	}
	if displayName == "" {
		displayName, _ = raw["preferred_username"].(string)
	}
	tenantID, _ := raw["tenant_id"].(string)
	return Claims{
		Subject:     sub,
		TenantID:    tenantID,
		ExpiresAt:   time.Unix(int64(expFloat), 0),
		DisplayName: displayName,
	}, nil
}
