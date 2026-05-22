package handler

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
)

type sessionsCursorPayload struct {
	StartedAt string `json:"started_at"`
	ID        string `json:"id"`
}

func encodeSessionsCursor(startedAt time.Time, id uuid.UUID) (string, error) {
	if startedAt.IsZero() {
		return "", errors.New("cursor started_at is zero")
	}
	if id == uuid.Nil {
		return "", errors.New("cursor id is nil")
	}
	payload := sessionsCursorPayload{
		StartedAt: startedAt.UTC().Format(time.RFC3339Nano),
		ID:        id.String(),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeSessionsCursor(raw string) (time.Time, uuid.UUID, error) {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("decode cursor: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var payload sessionsCursorPayload
	if err := dec.Decode(&payload); err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("decode cursor json: %w", err)
	}
	var trailing struct{}
	if err := dec.Decode(&trailing); err != io.EOF {
		return time.Time{}, uuid.Nil, errors.New("decode cursor json: trailing data")
	}
	startedAt, err := time.Parse(time.RFC3339Nano, payload.StartedAt)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("decode cursor started_at: %w", err)
	}
	if startedAt.IsZero() {
		return time.Time{}, uuid.Nil, errors.New("decode cursor started_at is zero")
	}
	id, err := uuid.Parse(payload.ID)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("decode cursor id: %w", err)
	}
	if id == uuid.Nil {
		return time.Time{}, uuid.Nil, errors.New("decode cursor id is nil")
	}
	return startedAt, id, nil
}
