package localauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuthorizeDevicePostsClientID(t *testing.T) {
	var sawClientID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/authorize" {
			t.Fatalf("path: got %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		sawClientID = r.Form.Get("client_id")
		writeJSON(t, w, map[string]any{
			"device_code":               "device",
			"user_code":                 "USER-CODE",
			"verification_uri":          "https://auth.example/device",
			"verification_uri_complete": "https://auth.example/device?user_code=USER-CODE",
			"expires_in":                300,
			"interval":                  5,
		})
	}))
	defer server.Close()

	client := NewClient(Config{
		ClientID:               "client_123",
		DeviceAuthorizationURL: server.URL + "/authorize",
		TokenURL:               server.URL + "/token",
	})
	auth, err := client.AuthorizeDevice(context.Background())
	if err != nil {
		t.Fatalf("AuthorizeDevice: %v", err)
	}
	if sawClientID != "client_123" {
		t.Fatalf("client_id: got %q", sawClientID)
	}
	if auth.UserCode != "USER-CODE" || auth.Interval != 5*time.Second {
		t.Fatalf("auth payload: %+v", auth)
	}
}

func TestPollTokenHandlesPendingThenSuccess(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if r.Form.Get("grant_type") != deviceCodeGrant {
			t.Fatalf("grant_type: got %q", r.Form.Get("grant_type"))
		}
		if calls == 1 {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(t, w, map[string]string{"error": "authorization_pending"})
			return
		}
		writeJSON(t, w, map[string]any{
			"access_token":  "access",
			"refresh_token": "refresh",
			"id_token":      "id",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer server.Close()

	client := NewClient(Config{
		ClientID:               "client_123",
		DeviceAuthorizationURL: server.URL + "/authorize",
		TokenURL:               server.URL,
	})
	client.Sleep = func(context.Context, time.Duration) error { return nil }
	client.Now = func() time.Time { return time.Unix(1_700_000_000, 0) }

	tokens, err := client.PollToken(context.Background(), DeviceAuthorization{
		DeviceCode: "device",
		ExpiresIn:  time.Minute,
		Interval:   time.Second,
		IssuedAt:   client.Now(),
	})
	if err != nil {
		t.Fatalf("PollToken: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls: got %d", calls)
	}
	if tokens.AccessToken != "access" || tokens.RefreshToken != "refresh" {
		t.Fatalf("tokens: %+v", tokens)
	}
}

func TestRemoteErrorsMapToSentinels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(t, w, map[string]string{"error": "slow_down"})
	}))
	defer server.Close()
	client := NewClient(Config{ClientID: "client", DeviceAuthorizationURL: server.URL, TokenURL: server.URL})

	_, err := client.DeviceToken(context.Background(), "device")
	if !errors.Is(err, ErrSlowDown) {
		t.Fatalf("err: got %v want ErrSlowDown", err)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("Encode: %v", err)
	}
}
