package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseTeamLimit(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		fallback int
		want     int
		wantOK   bool
	}{
		{"missing uses fallback", "/v1/dashboard/team", 50, 50, true},
		{"positive value", "/v1/dashboard/team?member_limit=25", 50, 25, true},
		{"cap at 100", "/v1/dashboard/team?member_limit=500", 50, 100, true},
		{"zero invalid", "/v1/dashboard/team?member_limit=0", 50, 0, false},
		{"negative invalid", "/v1/dashboard/team?member_limit=-1", 50, 0, false},
		{"non-integer invalid", "/v1/dashboard/team?member_limit=nope", 50, 0, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			got, ok := parseTeamLimit(req, "member_limit", tc.fallback)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.want {
				t.Fatalf("limit = %d, want %d", got, tc.want)
			}
		})
	}
}
