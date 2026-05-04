package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/browzeremb/browzer-cli/internal/version"
	wf "github.com/browzeremb/browzer-cli/internal/workflow"
)

// CurrentProtocolVersion is the JSON-RPC protocol version this daemon binary
// speaks. Bumped when the wire contract for `WorkflowMutate` (or any other
// method) changes in a non-additive way. The CLI's preflight handshake
// (`Daemon.Version`) compares this against its own constant; on mismatch the
// CLI falls back to the standalone path with a single stderr warning.
//
// History:
//   - v2 (2026-05-04, WF-SYNC-1): introduces the handshake. v1 daemons predate
//     the `Daemon.Version` method and are detected via `method_not_found`.
const CurrentProtocolVersion = 2

// CurrentSchemaVersion is the workflow.json schema version this daemon
// validates against. Mirrors the `schemaVersion` field of the schema-v2 CUE
// definition (packages/cli/schemas/workflow-v1.cue) and the equivalent skill
// references; bumped together as a single coordinated cutover.
const CurrentSchemaVersion = 2

// protocolFeatures is the static, lexicographically-sorted list of feature
// strings advertised by `Daemon.Version`. Sort order is enforced at compile
// time (the literal is already sorted) and pinned by the deterministic-JSON
// integration test. Adding a new feature requires keeping the slice sorted.
var protocolFeatures = []string{
	"jqVars",
	"save",
	"schemaV2",
}

// DaemonVersionResponse is the wire shape for the `Daemon.Version` JSON-RPC
// response. Field order is fixed (struct tag order is byte-stable in
// encoding/json's marshaling, so the integration test can compare two
// responses byte-by-byte).
type DaemonVersionResponse struct {
	DaemonVersion    string   `json:"daemonVersion"`
	SchemaVersion    int      `json:"schemaVersion"`
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

// WorkflowMutateParams is the wire shape for the WorkflowMutate method.
//
// Verb whitelist enforcement: handler rejects any verb not in
// wf.Mutators with `unknown_verb` BEFORE the queue is touched, so a
// malformed request never reaches a drainer goroutine.
//
// noLock is rejected unconditionally in the daemon path: when the daemon
// owns the queue, all writes go through the queue's drainer which always
// holds the advisory lock. Allowing `noLock=true` would let a caller
// bypass the lock the daemon itself just took, defeating the queue's
// FIFO guarantee against same-process readers.
type WorkflowMutateParams struct {
	Verb    string          `json:"verb"`
	Path    string          `json:"path"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Args    []string        `json:"args,omitempty"`
	JQExpr  string          `json:"jqExpr,omitempty"`
	// ProtocolVersion is the wire-protocol version the caller speaks. Required
	// for daemon-path calls since WF-SYNC-1 (2026-05-04). The daemon rejects
	// requests where ProtocolVersion != CurrentProtocolVersion with JSON-RPC
	// `-32602` ("invalid params") and a message describing client/daemon
	// versions. CLIs run a one-shot `Daemon.Version` preflight before the
	// first WorkflowMutate to detect mismatches and degrade to standalone.
	//
	// Default 0 is treated as "no version sent" → mismatch → reject. Older
	// CLIs that predate the field send 0 implicitly; they would not reach
	// this code path because their preflight (or `method_not_found` fallback)
	// already routed them to standalone.
	ProtocolVersion int `json:"protocolVersion,omitempty"`
	// JQVars binds variables for the patch verb's jq expression. Keys are
	// bare identifiers (no leading `$`); values are arbitrary JSON-decoded
	// scalars/objects/arrays. Used only when Verb=="patch". Older daemons
	// will silently ignore this field (additive contract, JSON unknown-field
	// tolerance).
	//
	// Version-skew failure mode: when a NEW CLI sends --arg/--argjson
	// bindings to an OLDER daemon binary that predates JQVars support, the
	// daemon silently drops the field and executes the jq expression without
	// any variable bindings. gojq then fails at compile time with
	// "undefined variable $<name>". The caller sees a cryptic jq error, not a
	// clear "daemon too old" message. Operators encountering this error should
	// restart the daemon to pick up the new binary:
	//
	//   browzer daemon stop && browzer daemon start
	JQVars          map[string]any  `json:"jqVars,omitempty"`
	NoLock          bool            `json:"noLock,omitempty"`
	AwaitDurability bool            `json:"awaitDurability,omitempty"`
	LockTimeoutMs   int64           `json:"lockTimeoutMs,omitempty"`
	WriteID         string          `json:"writeId,omitempty"`
}

// WorkflowMutateResult is the wire shape for the WorkflowMutate response.
type WorkflowMutateResult struct {
	WriteID         string `json:"writeId"`
	Mode            string `json:"mode"`
	StepID          string `json:"stepId,omitempty"`
	LockHeldMs      int64  `json:"lockHeldMs"`
	QueueDepthAhead int64  `json:"queueDepthAhead"`
	ValidatedOk     bool   `json:"validatedOk"`
	Durable         bool   `json:"durable"`
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
	// WorkflowMutate is registered eagerly in NewServer — see server.go
	// (it has no external Deps so it doesn't belong in Wire).
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
		SchemaVersion:    CurrentSchemaVersion,
		ProtocolFeatures: features,
		ProtocolVersion:  CurrentProtocolVersion,
	}, nil
}

// handleWorkflowMutate is the JSON-RPC entrypoint for workflow.json
// mutations. Lifecycle:
//
//  1. Parse + validate params (verb whitelist, abs path, noLock rejection).
//  2. Enqueue on the per-path FIFO via dispatcher.enqueue. Capture the
//     pre-send queue depth.
//  3. If awaitDurability=false: return immediately with mode="daemon-async".
//     The drainer will run the mutation; failures are silently lost from
//     the caller's POV (mirrors fire-and-forget semantics).
//  4. If awaitDurability=true: block on job.done with a ceiling of
//     lockTimeoutMs+2s to bound the handler in case the drainer hangs.
//     On success return mode="daemon-sync" with the durable+validatedOk
//     bits the drainer recorded.
//
// Errors mapped to JSON-RPC -32000 (server error) with stable string
// codes the client maps onto fallback decisions:
//
//	unknown_verb                    — caller must NOT retry; verb is bogus
//	invalid_params                  — malformed request
//	path_must_be_absolute           — relative paths rejected
//	noLock_unsupported_in_daemon_path — caller falls back to standalone
//	queue_full                       — caller falls back to standalone
//	timeout                          — sync wait deadline exceeded
//	(any wf.ApplyAndPersist error)   — propagated verbatim; caller surfaces
func (s *Server) handleWorkflowMutate(ctx context.Context, raw json.RawMessage) (any, error) {
	var p WorkflowMutateParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, errors.New("invalid_params")
	}
	// Protocol-version handshake (WF-SYNC-1, 2026-05-04). Reject any caller
	// that didn't run the `Daemon.Version` preflight or that speaks a
	// different protocol than this binary. Standard JSON-RPC `-32602`
	// (invalid params) lets the CLI tell this apart from a `-32000`
	// internal error and route to the standalone fallback.
	if p.ProtocolVersion != CurrentProtocolVersion {
		return nil, newCodedError(-32602, fmt.Sprintf(
			"protocol version mismatch: client=%d, daemon=%d",
			p.ProtocolVersion, CurrentProtocolVersion))
	}
	if _, ok := wf.Mutators[p.Verb]; !ok {
		return nil, fmt.Errorf("unknown_verb: %q", p.Verb)
	}
	if p.Path == "" || !filepath.IsAbs(p.Path) {
		return nil, errors.New("path_must_be_absolute")
	}
	if p.NoLock {
		return nil, errors.New("noLock_unsupported_in_daemon_path")
	}
	if s.workflowDispatcher == nil {
		return nil, errors.New("workflow_dispatcher_disabled")
	}

	lockTimeout := time.Duration(p.LockTimeoutMs) * time.Millisecond
	if lockTimeout <= 0 {
		lockTimeout = 5 * time.Second
	}

	job := &mutateJob{
		verb: p.Verb,
		path: p.Path,
		args: wf.MutatorArgs{
			Args:    p.Args,
			Payload: []byte(p.Payload),
			JQExpr:  p.JQExpr,
			JQVars:  p.JQVars,
		},
		awaitDurability: p.AwaitDurability,
		lockTimeout:     lockTimeout,
		writeID:         p.WriteID,
		enqueuedAt:      time.Now(),
		done:            make(chan struct{}),
	}

	depthAhead, enqErr := s.workflowDispatcher.enqueue(job)
	if enqErr != nil {
		return nil, enqErr
	}

	// Async: return immediately. The drainer owns job from here.
	if !p.AwaitDurability {
		return WorkflowMutateResult{
			WriteID:         p.WriteID,
			Mode:            "daemon-async",
			QueueDepthAhead: int64(depthAhead),
		}, nil
	}

	// Sync: block on done with a bounded ceiling. The +2s slack absorbs
	// the lock-acquire-then-write window past the lock acquisition timeout.
	ceiling := lockTimeout + 2*time.Second
	select {
	case <-job.done:
		// fallthrough.
	case <-time.After(ceiling):
		return nil, errors.New("timeout")
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if job.err != nil {
		return nil, job.err
	}
	return WorkflowMutateResult{
		WriteID:         p.WriteID,
		Mode:            "daemon-sync",
		StepID:          job.result.StepID,
		LockHeldMs:      job.lockHeld.Milliseconds(),
		QueueDepthAhead: int64(depthAhead),
		ValidatedOk:     job.result.ValidatedOk,
		Durable:         job.result.Durable,
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
