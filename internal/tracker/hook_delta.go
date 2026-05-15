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
	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
)

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
	hook := args.Hook
	if hook == "" {
		return
	}
	savedTokens := args.SavedTokens
	if savedTokens == 0 {
		return
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

	payload := map[string]any{
		"ts":               "",
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
	}

	// Fire-and-forget: TrackEvent already handles the daemon-unreachable
	// fallback path (pending-events JSONL + detached respawn).
	internalhooks.TrackEvent(payload)
}
