package respond

import (
	"encoding/json"
	"net/http"
)

// Error is the shared REST error envelope for API and dashboard handlers.
type Error struct {
	Error        string   `json:"error"`
	Details      []string `json:"details,omitempty"`
	See          string   `json:"see,omitempty"`
	RequiredRole string   `json:"required_role,omitempty"`
}

// JSON writes a no-store JSON response. The dashboard endpoints expose
// tenant/user-scoped state, so the conservative cache policy is shared here.
func JSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
