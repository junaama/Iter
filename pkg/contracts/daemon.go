package contracts

import "time"

// DaemonStatusResponse mirrors contracts.py DaemonStatusResponse for local
// app-to-daemon IPC over the launchd Unix socket.
type DaemonStatusResponse struct {
	Running       bool       `json:"running"`
	CurrentTask   *string    `json:"current_task,omitempty"`
	IdleSince     *time.Time `json:"idle_since,omitempty"`
	LastSessionAt *time.Time `json:"last_session_at,omitempty"`
	CapturedToday int        `json:"captured_today"`
	Paused        bool       `json:"paused"`
}
