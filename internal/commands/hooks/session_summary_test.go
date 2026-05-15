package hooks

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

// withGainOutput is a test-local convenience wrapper around the exported
// WithGainOutput seam. It registers a t.Cleanup to restore the original.
func withGainOutput(t *testing.T, fn func() ([]byte, error)) {
	t.Helper()
	restore := WithGainOutput(fn)
	t.Cleanup(restore)
}

// gainJSON is a convenience helper for building `browzer gain --adoption --json`
// payloads inline in test cases.
func gainJSON(savedTotal, wastedTotal int64, adoption *float64, topSource string, topTokens int64) []byte {
	payload := map[string]any{
		"since":       "1h",
		"savedTotal":  savedTotal,
		"wastedTotal": wastedTotal,
		"adoption":    adoption,
		"byPattern": map[string]any{
			"savedFromCli":   savedTotal,
			"savedFromHooks": 0,
			"wasted":         wastedTotal,
			"other":          0,
		},
	}
	if topSource != "" {
		payload["topWasted"] = map[string]any{
			"source":      topSource,
			"command":     topSource,
			"savedTokens": -topTokens,
			"suggestion":  "browzer explore \"<query>\" --json --save /tmp/explore.json",
		}
	}
	b, _ := json.Marshal(payload)
	return b
}

// parseAdditionalContext is a helper that decodes the sessionSummaryOutput
// struct from JSON written to the session-summary envelope.
func parseAdditionalContext(t *testing.T, out *sessionSummaryOutput) string {
	t.Helper()
	if out == nil {
		t.Fatal("expected non-nil output envelope, got nil")
	}
	return out.AdditionalContext
}

// TestSessionSummary_T1_RealCLIShape mirrors T-1 from the JS test file:
// non-active stop + real CLI JSON shape → output contains "[browzer] Session"
// and reflects savedTotal + topWasted.
func TestSessionSummary_T1_RealCLIShape(t *testing.T) {
	setupWorkspaceFixture(t)

	adoption := 3.5
	withGainOutput(t, func() ([]byte, error) {
		return gainJSON(42000, 12000, &adoption, "wasted-rg", 8000), nil
	})

	in := []byte(`{"stop_hook_active":false,"session_id":"test-t1"}`)
	out := sessionSummaryDecide(in)

	ctx := parseAdditionalContext(t, out)

	if len(ctx) == 0 {
		t.Fatal("additionalContext must not be empty")
	}

	// Must contain the session header.
	if !containsSubstr(ctx, "[browzer] Session") {
		t.Errorf("additionalContext must contain \"[browzer] Session\", got: %q", ctx)
	}
	// Saved tokens rounded to k.
	if !containsSubstr(ctx, "42k tokens") {
		t.Errorf("expected \"42k tokens\" in output, got: %q", ctx)
	}
	// Adoption ratio.
	if !containsSubstr(ctx, "adoption 3.5x") {
		t.Errorf("expected \"adoption 3.5x\" in output, got: %q", ctx)
	}
	// Top wasted source.
	if !containsSubstr(ctx, "wasted-rg") {
		t.Errorf("expected \"wasted-rg\" in output, got: %q", ctx)
	}
	// Wasted magnitude rounded to k (absolute value of -8000 = 8000 → 8k).
	if !containsSubstr(ctx, "8k") {
		t.Errorf("expected \"8k\" in output, got: %q", ctx)
	}
}

// TestSessionSummary_T2_StopHookActive mirrors T-2: stop_hook_active=true
// → no output emitted.
func TestSessionSummary_T2_StopHookActive(t *testing.T) {
	setupWorkspaceFixture(t)

	adoption := 1.0
	withGainOutput(t, func() ([]byte, error) {
		return gainJSON(10000, 10000, &adoption, "", 0), nil
	})

	in := []byte(`{"stop_hook_active":true,"session_id":"test-t2"}`)
	out := sessionSummaryDecide(in)

	if out != nil {
		t.Errorf("expected nil output when stop_hook_active=true, got: %+v", out)
	}
}

// TestSessionSummary_T3_GainOutputIgnoredOutsideWorkspace mirrors T-3:
// when not in a Browzer workspace the gain subprocess should not be consulted.
// We verify by injecting a gain seam that returns 99999 tokens — if the
// guard incorrectly runs it, the output would contain "99k tokens".
func TestSessionSummary_T3_GainOutputIgnoredOutsideWorkspace(t *testing.T) {
	// Redirect HOME to a temp dir that has no .browzer/credentials so
	// IsInBrowzerWorkspace returns false regardless of the host machine state.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Move cwd away from any directory that might contain a .browzer/config.json.
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir tmp: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	called := false
	withGainOutput(t, func() ([]byte, error) {
		called = true
		v := 99999.0
		return gainJSON(99999, 0, &v, "", 0), nil
	})

	in := []byte(`{"stop_hook_active":false,"session_id":"test-t3"}`)
	out := sessionSummaryDecide(in)

	if called {
		t.Error("gain subprocess must not be invoked outside a Browzer workspace")
	}
	if out != nil {
		t.Errorf("expected nil output outside workspace, got: %+v", out)
	}
}

// TestSessionSummary_T4_SilentExitOnGainFailure mirrors T-4: when the
// gain subprocess fails (browzer not on PATH or timeout) the guard must
// exit silently with no output.
func TestSessionSummary_T4_SilentExitOnGainFailure(t *testing.T) {
	setupWorkspaceFixture(t)

	withGainOutput(t, func() ([]byte, error) {
		return nil, &execNotFoundError{}
	})

	in := []byte(`{"stop_hook_active":false,"session_id":"test-t4"}`)
	out := sessionSummaryDecide(in)

	if out != nil {
		t.Errorf("expected nil output when gain fails, got: %+v", out)
	}
}

// TestSessionSummary_T5_MalformedJSON mirrors T-5: when the gain subprocess
// emits non-JSON output the guard emits the safe fallback string.
func TestSessionSummary_T5_MalformedJSON(t *testing.T) {
	setupWorkspaceFixture(t)

	withGainOutput(t, func() ([]byte, error) {
		return []byte("not-valid-json{"), nil
	})

	in := []byte(`{"stop_hook_active":false,"session_id":"test-t5"}`)
	out := sessionSummaryDecide(in)

	if out == nil {
		t.Fatal("expected non-nil output for malformed JSON fallback path")
	}
	if !containsSubstr(out.AdditionalContext, "Session summary unavailable") {
		t.Errorf("expected fallback string, got: %q", out.AdditionalContext)
	}
}

// TestSessionSummary_EmptySession verifies that a gain report with
// savedTotal=0 and no topWasted still produces a valid summary line.
func TestSessionSummary_EmptySession(t *testing.T) {
	setupWorkspaceFixture(t)

	withGainOutput(t, func() ([]byte, error) {
		return gainJSON(0, 0, nil, "", 0), nil
	})

	in := []byte(`{"stop_hook_active":false,"session_id":"test-empty"}`)
	out := sessionSummaryDecide(in)

	if out == nil {
		t.Fatal("expected non-nil output for zero-token session")
	}
	ctx := out.AdditionalContext
	if !containsSubstr(ctx, "[browzer] Session saved 0k tokens") {
		t.Errorf("expected zero-token summary, got: %q", ctx)
	}
}

// TestSessionSummary_NoAdoptionWhenInfinity verifies that an infinite
// adoption ratio (wastedTotal=0) omits the adoption parenthetical.
func TestSessionSummary_NoAdoptionWhenInfinity(t *testing.T) {
	setupWorkspaceFixture(t)

	// Simulate the JSON payload the Go CLI emits: adoption=null when wastedTotal=0.
	payload := []byte(`{"since":"1h","savedTotal":5000,"wastedTotal":0,"adoption":null,"topWasted":null}`)
	withGainOutput(t, func() ([]byte, error) {
		return payload, nil
	})

	in := []byte(`{"stop_hook_active":false,"session_id":"test-inf"}`)
	out := sessionSummaryDecide(in)

	if out == nil {
		t.Fatal("expected non-nil output")
	}
	ctx := out.AdditionalContext
	if containsSubstr(ctx, "adoption") {
		t.Errorf("adoption part must be omitted when ratio is null, got: %q", ctx)
	}
}

// TestSessionSummary_SnakeCaseAliases verifies that snake_case field names
// emitted by older CLI builds are tolerated.
func TestSessionSummary_SnakeCaseAliases(t *testing.T) {
	setupWorkspaceFixture(t)

	payload := []byte(`{"saved_total":20000,"wasted_total":5000,"adoption":4.0,"top_wasted":{"source":"alias-source","saved_tokens":-3000}}`)
	withGainOutput(t, func() ([]byte, error) {
		return payload, nil
	})

	in := []byte(`{"stop_hook_active":false}`)
	out := sessionSummaryDecide(in)

	if out == nil {
		t.Fatal("expected non-nil output for snake_case gain payload")
	}
	ctx := out.AdditionalContext
	if !containsSubstr(ctx, "20k tokens") {
		t.Errorf("expected \"20k tokens\", got: %q", ctx)
	}
	if !containsSubstr(ctx, "adoption 4.0x") {
		t.Errorf("expected \"adoption 4.0x\", got: %q", ctx)
	}
	if !containsSubstr(ctx, "alias-source") {
		t.Errorf("expected \"alias-source\" in top wasted, got: %q", ctx)
	}
}

// TestSessionSummary_MultiSkillExecution verifies a session with multiple
// skills executed (non-trivial savedTotal and topWasted).
func TestSessionSummary_MultiSkillExecution(t *testing.T) {
	setupWorkspaceFixture(t)

	adoption := 2.1
	withGainOutput(t, func() ([]byte, error) {
		return gainJSON(150000, 71000, &adoption, "wasted-glob", 25000), nil
	})

	in := []byte(`{"stop_hook_active":false,"session_id":"test-multi-skill"}`)
	out := sessionSummaryDecide(in)

	ctx := parseAdditionalContext(t, out)
	if !containsSubstr(ctx, "150k tokens") {
		t.Errorf("expected \"150k tokens\", got: %q", ctx)
	}
	if !containsSubstr(ctx, "adoption 2.1x") {
		t.Errorf("expected \"adoption 2.1x\", got: %q", ctx)
	}
	if !containsSubstr(ctx, "wasted-glob") {
		t.Errorf("expected \"wasted-glob\", got: %q", ctx)
	}
	if !containsSubstr(ctx, "25k") {
		t.Errorf("expected \"25k\" for wasted magnitude, got: %q", ctx)
	}
}

// TestFormatGainSummary_RoundToK unit-tests roundToK directly.
func TestFormatGainSummary_RoundToK(t *testing.T) {
	cases := []struct {
		input    int64
		expected int64
	}{
		{0, 0},
		{999, 1},
		{1000, 1},
		{1500, 2},
		{42000, 42},
		{-8000, -8},
	}
	for _, c := range cases {
		got := roundToK(c.input)
		if got != c.expected {
			t.Errorf("roundToK(%d) = %d, want %d", c.input, got, c.expected)
		}
	}
}

// TestFormatGainSummary_InfiniteAdoptionOmitted verifies formatGainSummary
// omits the adoption part when Adoption is +Inf.
func TestFormatGainSummary_InfiniteAdoptionOmitted(t *testing.T) {
	inf := math.Inf(1)
	r := &gainReport{
		SavedTotal: int64Ptr(5000),
		Adoption:   &inf,
	}
	got := formatGainSummary(r)
	if containsSubstr(got, "adoption") {
		t.Errorf("adoption must be omitted for +Inf, got: %q", got)
	}
}

// containsSubstr is a simple helper used throughout the test file.
func containsSubstr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		findSubstr(s, sub))
}

func findSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// int64Ptr is a tiny helper for test literals.
func int64Ptr(v int64) *int64 { return &v }

// execNotFoundError is a stub error that satisfies the error interface,
// used by T-4 to simulate a gain subprocess failure.
type execNotFoundError struct{}

func (e *execNotFoundError) Error() string { return "exec: browzer not found" }
