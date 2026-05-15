package daemon

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/browzeremb/browzer-cli/internal/version"
)

// CurrentProtocolVersion is the JSON-RPC protocol version this daemon binary
// speaks. Bumped when the wire contract for any method changes in a
// non-additive way. The CLI's preflight handshake (`Daemon.Version`) compares
// this against its own constant; on mismatch the CLI falls back without
// sending requests that may rely on the missing surface.
const CurrentProtocolVersion = 3

// protocolFeatures is the static, lexicographically-sorted list of feature
// strings advertised by `Daemon.Version`. Sort order is enforced at compile
// time (the literal is already sorted) and pinned by the deterministic-JSON
// integration test. Adding a new feature requires keeping the slice sorted.
var protocolFeatures = []string{
	"estimationMethod",
}

// DaemonVersionResponse is the wire shape for the `Daemon.Version` JSON-RPC
// response. Field order is fixed (struct tag order is byte-stable in
// encoding/json's marshaling, so the integration test can compare two
// responses byte-by-byte).
type DaemonVersionResponse struct {
	DaemonVersion    string   `json:"daemonVersion"`
	ProtocolFeatures []string `json:"protocolFeatures"`
	ProtocolVersion  int      `json:"protocolVersion"`
}

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
	// EstimationMethod classifies how SavedTokens was derived (FR-7).
	// One of: 'measured' | 'estimated' | 'counterfactual' | 'unknown'.
	// Older clients omit the field; daemon records it as NULL.
	EstimationMethod *string `json:"estimationMethod,omitempty"`
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

// HealthResponse is the wire shape for the Health method, exported so the
// client can decode it. (The historic anonymous map[string]any return value
// is preserved for backwards compat — handler still emits the same fields.)
type HealthResponse struct {
	UptimeSec    int      `json:"uptimeSec"`
	QueueLen     int64    `json:"queueLen"`
	DBPath       string   `json:"dbPath"`
	Capabilities []string `json:"capabilities,omitempty"`
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

// handleDaemonVersion answers the `Daemon.Version` JSON-RPC method. The
// response is byte-deterministic across invocations on a given binary:
// `protocolFeatures` is returned in stable lexicographic order from the
// package-level slice; struct field order maps to JSON key order via the
// encoding/json marshaler. Tests pin this by marshaling two consecutive
// responses and comparing byte slices.
//
// `daemonVersion` reads from `internal/version.Version` (ldflag-injected by
// goreleaser; "" in dev/test builds). An empty string is acceptable — the
// CLI's preflight only inspects `protocolVersion` for the mismatch decision.
func (s *Server) handleDaemonVersion(_ context.Context, _ json.RawMessage) (any, error) {
	// Defensive copy so a misbehaving caller can't mutate the package-level
	// slice via a marshaled-then-unmarshaled round trip on the same process.
	features := make([]string, len(protocolFeatures))
	copy(features, protocolFeatures)
	return DaemonVersionResponse{
		DaemonVersion:    version.Version,
		ProtocolFeatures: features,
		ProtocolVersion:  CurrentProtocolVersion,
	}, nil
}

// Deps is the dependency surface the daemon needs from outside the
// package (manifest cache, filter engine, session cache, tracker stub).
// The Tracking plan replaces the no-op tracker with the SQLite one.
type Deps struct {
	Read            func(context.Context, ReadParams) (ReadResult, error)
	Track           func(context.Context, TrackParams) (map[string]any, error)
	SessionRegister func(context.Context, SessionRegisterParams) (SessionRegisterResult, error)
}
