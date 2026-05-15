package tracker

import (
	"testing"
	"time"
)

// TestBuildHookDeltaPayload_TimestampIsNonEmpty pins the regression
// surfaced live in 2026-05-15: the JS-era predecessor left `ts` as an
// empty string, which made `gain` / `gain --hooks` silently filter
// every EmitHookDelta-sourced event out of the report (the SQL cutoff
// `ts >= ?` excludes empty strings — they sort before any RFC3339
// timestamp). EmitHookDelta MUST stamp every payload with a current
// wall-clock UTC RFC3339 timestamp.
func TestBuildHookDeltaPayload_TimestampIsNonEmpty(t *testing.T) {
	payload, ok := buildHookDeltaPayload(HookDeltaArgs{
		Hook:        "wasted-grep",
		SavedTokens: -123,
	})
	if !ok {
		t.Fatalf("expected ok=true for non-empty hook + non-zero savedTokens")
	}
	ts, _ := payload["ts"].(string)
	if ts == "" {
		t.Fatalf("ts MUST NOT be empty — gain WHERE ts >= cutoff excludes empty strings")
	}
	if _, err := time.Parse(time.RFC3339, ts); err != nil {
		t.Fatalf("ts must be RFC3339-parseable; got %q: %v", ts, err)
	}
}

// TestBuildHookDeltaPayload_UsesClockSeam confirms tests can swap the
// time source for deterministic assertions.
func TestBuildHookDeltaPayload_UsesClockSeam(t *testing.T) {
	saved := nowRFC3339
	defer func() { nowRFC3339 = saved }()
	const fixed = "2026-05-15T12:00:00Z"
	nowRFC3339 = func() string { return fixed }

	payload, ok := buildHookDeltaPayload(HookDeltaArgs{
		Hook:        "wasted-find",
		SavedTokens: -42,
	})
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if got, _ := payload["ts"].(string); got != fixed {
		t.Fatalf("ts: got %q, want %q", got, fixed)
	}
}

// TestBuildHookDeltaPayload_ZeroOrEmptyDropped confirms the validation
// contract — zero savings or empty hook silently no-ops (mirrors the
// JS guard's behaviour exactly).
func TestBuildHookDeltaPayload_ZeroOrEmptyDropped(t *testing.T) {
	cases := []struct {
		name string
		args HookDeltaArgs
	}{
		{"empty hook", HookDeltaArgs{Hook: "", SavedTokens: 100}},
		{"zero saved tokens", HookDeltaArgs{Hook: "wasted-grep", SavedTokens: 0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, ok := buildHookDeltaPayload(c.args); ok {
				t.Fatalf("expected ok=false for %s", c.name)
			}
		})
	}
}
