// Package tracker — hook delta emitter.
//
// EmitHookDelta records a single "hook saved/spent tokens" entry via
// internal/hooks.TrackEvent with the canonical source bucket
// "hook-saved-tokens". It mirrors the behaviour of the retired JS helper
// _emit-hook-delta.mjs so the Go subcommands can call it directly without
// routing through the Node layer.
//
// The call is fire-and-forget: errors from TrackEvent are intentionally
// swallowed so a telemetry failure never stalls the host hook.
package tracker

import (
	"time"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
)

// nowRFC3339 is the time-source seam for EmitHookDelta. Production
// stamps each Track payload with the current wall-clock UTC time so
// `browzer gain` (whose query filters by `ts >= cutoff`) actually sees
// the event. Tests may swap this to a fixed clock if they need
// deterministic timestamps.
var nowRFC3339 = func() string { return time.Now().UTC().Format(time.RFC3339) }

// hookSavedTokensBucket is the canonical source label aggregated by
// `browzer gain --hooks --json`.
const hookSavedTokensBucket = "hook-saved-tokens"

// HookDeltaArgs carries the parameters for a single hook token-delta event.
// Only Hook and SavedTokens are required; all others are optional metadata.
type HookDeltaArgs struct {
	// Hook is the logical hook name written to the tracker's command column.
	Hook string
	// SavedTokens is the token delta to record (integer). Positive = saved;
	// negative = wasted. Zero values are silently dropped.
	SavedTokens int
	// SessionID is an optional session identifier pass-through.
	SessionID string
	// PathHash is an optional 12-char path digest for correlating events.
	PathHash string
	// ExecMs is the wall-clock execution duration in milliseconds.
	ExecMs int
	// EstimationMethod classifies how SavedTokens was derived.
	// One of: 'measured', 'estimated', 'counterfactual'. Defaults to
	// 'estimated' when empty.
	EstimationMethod string
}

// EmitHookDelta records one hook token-delta event.  No-ops on empty Hook or
// zero SavedTokens — mirrors the JS guard's validation contract exactly.
func EmitHookDelta(args HookDeltaArgs) {
	payload, ok := buildHookDeltaPayload(args)
	if !ok {
		return
	}
	// Fire-and-forget: TrackEvent already handles the daemon-unreachable
	// fallback path (pending-events JSONL + detached respawn).
	internalhooks.TrackEvent(payload)
}

// buildHookDeltaPayload composes the Track payload that EmitHookDelta
// emits. Split from EmitHookDelta so tests can verify the wire shape
// without standing up a daemon socket. Returns ok=false when the args
// would be silently dropped (empty Hook or zero SavedTokens) — same
// validation contract as EmitHookDelta itself.
func buildHookDeltaPayload(args HookDeltaArgs) (map[string]any, bool) {
	hook := args.Hook
	if hook == "" {
		return nil, false
	}
	savedTokens := args.SavedTokens
	if savedTokens == 0 {
		return nil, false
	}

	estimationMethod := args.EstimationMethod
	if estimationMethod == "" {
		estimationMethod = "estimated"
	}

	// Build sessionId and pathHash as nullable: pass nil when empty so the
	// tracker schema stores NULL rather than an empty string.
	var sessionID, pathHash any
	if args.SessionID != "" {
		sessionID = args.SessionID
	}
	if args.PathHash != "" {
		pathHash = args.PathHash
	}

	return map[string]any{
		// ts: RFC3339 wall-clock at emit time. The empty-string default
		// the JS predecessor shipped left `gain` blind to every
		// EmitHookDelta event because the SQL `WHERE ts >= ?` cutoff
		// excludes empty strings (lexicographically less than any
		// non-empty timestamp).
		"ts":               nowRFC3339(),
		"source":           hookSavedTokensBucket,
		"command":          hook,
		"inputBytes":       0,
		"outputBytes":      0,
		"savedTokens":      savedTokens,
		"savingsPct":       float64(0),
		"filterLevel":      nil,
		"filterFailed":     false,
		"execMs":           args.ExecMs,
		"sessionId":        sessionID,
		"pathHash":         pathHash,
		"estimationMethod": estimationMethod,
	}, true
}
