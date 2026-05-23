package localauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const deviceCodeGrant = "urn:ietf:params:oauth:grant-type:device_code"

var (
	ErrAccessDenied         = errors.New("access denied")
	ErrAuthorizationPending = errors.New("authorization pending")
	ErrAuthorizationTimeout = errors.New("authorization timed out")
	ErrExpiredToken         = errors.New("expired token")
	ErrInvalidResponse      = errors.New("invalid auth response")
	ErrMissingClientID      = errors.New("missing WorkOS client id")
	ErrSlowDown             = errors.New("slow down")
)

type Client struct {
	Config Config
	HTTP   *http.Client
	Sleep  func(context.Context, time.Duration) error
	Now    func() time.Time
}

type DeviceAuthorization struct {
	DeviceCode              string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresIn               time.Duration
	Interval                time.Duration
	IssuedAt                time.Time
}

type TokenResponse struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	TokenType    string
	ExpiresIn    time.Duration
}

func NewClient(cfg Config) *Client {
	return &Client{
		Config: cfg,
		HTTP:   http.DefaultClient,
		Sleep:  sleepContext,
		Now:    time.Now,
	}
}

func (c *Client) AuthorizeDevice(ctx context.Context) (DeviceAuthorization, error) {
	if err := c.Config.Validate(); err != nil {
		return DeviceAuthorization{}, err
	}
	var payload deviceAuthorizationPayload
	if err := c.postForm(ctx, c.Config.DeviceAuthorizationURL, url.Values{
		"client_id": {c.Config.ClientID},
	}, &payload); err != nil {
		return DeviceAuthorization{}, err
	}
	if payload.DeviceCode == "" || payload.UserCode == "" || payload.VerificationURI == "" {
		return DeviceAuthorization{}, ErrInvalidResponse
	}
	return DeviceAuthorization{
		DeviceCode:              payload.DeviceCode,
		UserCode:                payload.UserCode,
		VerificationURI:         payload.VerificationURI,
		VerificationURIComplete: payload.VerificationURIComplete,
		ExpiresIn:               time.Duration(payload.ExpiresIn) * time.Second,
		Interval:                time.Duration(payload.Interval) * time.Second,
		IssuedAt:                c.now(),
	}, nil
}

func (c *Client) PollToken(ctx context.Context, auth DeviceAuthorization) (TokenResponse, error) {
	delay := auth.Interval
	if delay <= 0 {
		delay = 5 * time.Second
	}
	deadline := auth.IssuedAt.Add(auth.ExpiresIn)
	if auth.IssuedAt.IsZero() {
		deadline = c.now().Add(auth.ExpiresIn)
	}

	for c.now().Before(deadline) {
		tokens, err := c.DeviceToken(ctx, auth.DeviceCode)
		switch {
		case err == nil:
			return tokens, nil
		case errors.Is(err, ErrAuthorizationPending):
			if err := c.sleep(ctx, delay); err != nil {
				return TokenResponse{}, err
			}
			delay = minDuration(time.Duration(float64(delay)*1.5), 15*time.Second)
		case errors.Is(err, ErrSlowDown):
			delay = minDuration(delay+5*time.Second, 20*time.Second)
			if err := c.sleep(ctx, delay); err != nil {
				return TokenResponse{}, err
			}
		default:
			return TokenResponse{}, err
		}
	}
	return TokenResponse{}, ErrAuthorizationTimeout
}

func (c *Client) DeviceToken(ctx context.Context, deviceCode string) (TokenResponse, error) {
	return c.requestToken(ctx, url.Values{
		"grant_type":  {deviceCodeGrant},
		"device_code": {deviceCode},
		"client_id":   {c.Config.ClientID},
	})
}

func (c *Client) Refresh(ctx context.Context, refreshToken string) (TokenResponse, error) {
	return c.requestToken(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {c.Config.ClientID},
	})
}

func (c *Client) requestToken(ctx context.Context, fields url.Values) (TokenResponse, error) {
	if err := c.Config.Validate(); err != nil {
		return TokenResponse{}, err
	}
	var payload tokenPayload
	if err := c.postForm(ctx, c.Config.TokenURL, fields, &payload); err != nil {
		return TokenResponse{}, err
	}
	if payload.AccessToken == "" || payload.RefreshToken == "" {
		return TokenResponse{}, ErrInvalidResponse
	}
	return TokenResponse{
		AccessToken:  payload.AccessToken,
		RefreshToken: payload.RefreshToken,
		IDToken:      payload.IDToken,
		TokenType:    payload.TokenType,
		ExpiresIn:    time.Duration(payload.ExpiresIn) * time.Second,
	}, nil
}

func (c *Client) postForm(ctx context.Context, endpoint string, fields url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(fields.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var payload errorPayload
		if err := json.Unmarshal(body, &payload); err == nil && payload.Error != "" {
			return remoteError(payload.Error)
		}
		return fmt.Errorf("workos http %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}
	return nil
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	return sleepContext(ctx, d)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func remoteError(code string) error {
	switch code {
	case "authorization_pending":
		return ErrAuthorizationPending
	case "slow_down":
		return ErrSlowDown
	case "access_denied":
		return ErrAccessDenied
	case "expired_token":
		return ErrExpiredToken
	default:
		return fmt.Errorf("workos error: %s", code)
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

type deviceAuthorizationPayload struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type tokenPayload struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

type errorPayload struct {
	Error string `json:"error"`
}
