package daemon

import (
	"context"
	"encoding/json"
	"errors"
)

// ReadParams is the wire shape for the Read method.
// See packages/cli/internal/daemon/contract.md.
type ReadParams struct {
	Path        string  `json:"path"`
	FilterLevel string  `json:"filterLevel"`
	Offset      *int    `json:"offset,omitempty"`
	Limit       *int    `json:"limit,omitempty"`
	SessionID   *string `json:"sessionId,omitempty"`
	Model       *string `json:"model,omitempty"`
	// WorkspaceID is the canonical workspace UUID the file belongs to,
	// read by the caller from `.browzer/config.json` at the workspace
	// root. When present, the daemon consults the per-workspace manifest
	// cache to drive `filterLevel: "aggressive"`. When omitted, the daemon
	// downgrades aggressive to minimal (strip comments only).
	WorkspaceID *string `json:"workspaceId,omitempty"`
}

// ReadResult is the wire shape for the Read response.
type ReadResult struct {
	TempPath     string `json:"tempPath"`
	SavedTokens  int    `json:"savedTokens"`
	Filter       string `json:"filter"`
	FilterFailed bool   `json:"filterFailed"`
}

// TrackParams matches the SQLite events schema in spec §5.1.
type TrackParams struct {
	TS           string  `json:"ts"`
	Source       string  `json:"source"`
	Command      string  `json:"command"`
	PathHash     *string `json:"pathHash,omitempty"`
	InputBytes   int     `json:"inputBytes"`
	OutputBytes  int     `json:"outputBytes"`
	SavedTokens  int     `json:"savedTokens"`
	SavingsPct   float64 `json:"savingsPct"`
	FilterLevel  *string `json:"filterLevel,omitempty"`
	ExecMs       int     `json:"execMs"`
	WorkspaceID  *string `json:"workspaceId,omitempty"`
	SessionID    *string `json:"sessionId,omitempty"`
	Model        *string `json:"model,omitempty"`
	FilterFailed bool    `json:"filterFailed"`
}

// SessionRegisterParams identifies a session and the path to its transcript.
type SessionRegisterParams struct {
	SessionID      string `json:"sessionId"`
	TranscriptPath string `json:"transcriptPath"`
}

// SessionRegisterResult returns the resolved model (or null).
type SessionRegisterResult struct {
	Model *string `json:"model"`
}

// Wire installs Read/Track/SessionRegister on the server. Called from
// the daemon entrypoint with the live dependencies.
func (s *Server) Wire(deps Deps) {
	s.RegisterHandler("Read", func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p ReadParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errors.New("invalid_params")
		}
		return deps.Read(ctx, p)
	})
	s.RegisterHandler("Track", func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p TrackParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errors.New("invalid_params")
		}
		return deps.Track(ctx, p)
	})
	s.RegisterHandler("SessionRegister", func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p SessionRegisterParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errors.New("invalid_params")
		}
		return deps.SessionRegister(ctx, p)
	})
}

// Deps is the dependency surface the daemon needs from outside the
// package (manifest cache, filter engine, session cache, tracker stub).
// The Tracking plan replaces the no-op tracker with the SQLite one.
type Deps struct {
	Read            func(context.Context, ReadParams) (ReadResult, error)
	Track           func(context.Context, TrackParams) (map[string]any, error)
	SessionRegister func(context.Context, SessionRegisterParams) (SessionRegisterResult, error)
}
