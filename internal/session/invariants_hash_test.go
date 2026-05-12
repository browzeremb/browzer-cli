package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHashInvariants_Deterministic ensures the same input always produces the
// same hash (FR-5).
func TestHashInvariants_Deterministic(t *testing.T) {
	block := "- invariant one\n- invariant two\n"
	h1 := HashInvariants(block)
	h2 := HashInvariants(block)
	if h1 != h2 {
		t.Fatalf("HashInvariants not deterministic: %q vs %q", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("expected 64-char hex SHA-256, got len=%d: %q", len(h1), h1)
	}
}

// TestHashInvariants_ChangesOnDifferentInput ensures different blocks produce
// different hashes.
func TestHashInvariants_ChangesOnDifferentInput(t *testing.T) {
	h1 := HashInvariants("- invariant one\n")
	h2 := HashInvariants("- invariant one\n- invariant two\n")
	if h1 == h2 {
		t.Error("expected different hashes for different invariant blocks")
	}
}

// TestShouldEmitFull_ModeFullAlwaysTrue ensures mode=full always returns true.
func TestShouldEmitFull_ModeFullAlwaysTrue(t *testing.T) {
	// Set up a temporary BROWZER_HOME so cache writes don't pollute the real home.
	dir := t.TempDir()
	t.Setenv("BROWZER_HOME", dir)

	sid := "test-session-full"
	hash := HashInvariants("block")

	// Even if we record the same hash, full mode always returns true.
	RecordHash(sid, hash)
	if !ShouldEmitFull(sid, hash, InvariantsModeFull) {
		t.Error("mode=full: ShouldEmitFull should always return true")
	}
}

// TestShouldEmitFull_ModeSkipAlwaysFalse ensures mode=skip always returns false.
func TestShouldEmitFull_ModeSkipAlwaysFalse(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BROWZER_HOME", dir)

	sid := "test-session-skip"
	hash := HashInvariants("block")

	if ShouldEmitFull(sid, hash, InvariantsModeSkip) {
		t.Error("mode=skip: ShouldEmitFull should always return false")
	}
}

// TestShouldEmitFull_ModeHashAlwaysFalse ensures mode=hash always returns false.
func TestShouldEmitFull_ModeHashAlwaysFalse(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BROWZER_HOME", dir)

	sid := "test-session-hash-mode"
	hash := HashInvariants("block")

	if ShouldEmitFull(sid, hash, InvariantsModeHash) {
		t.Error("mode=hash: ShouldEmitFull should always return false")
	}
}

// TestShouldEmitFull_AutoFirstCallEmitsFull verifies that the first call in a
// session (empty cache) returns true.
func TestShouldEmitFull_AutoFirstCallEmitsFull(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BROWZER_HOME", dir)

	sid := "test-session-first"
	hash := HashInvariants("some invariants block")

	if !ShouldEmitFull(sid, hash, InvariantsModeAuto) {
		t.Error("first call (empty cache): ShouldEmitFull should return true")
	}
}

// TestShouldEmitFull_AutoSubsequentCallEmitsMarker verifies that after
// recording the hash, a second call returns false (emit marker only).
func TestShouldEmitFull_AutoSubsequentCallEmitsMarker(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BROWZER_HOME", dir)

	sid := "test-session-subsequent"
	block := "- invariant A\n- invariant B\n"
	hash := HashInvariants(block)

	// First call → full (returns true); record hash.
	if !ShouldEmitFull(sid, hash, InvariantsModeAuto) {
		t.Fatal("first call should return true")
	}
	RecordHash(sid, hash)

	// Second call with same block → marker only (returns false).
	if ShouldEmitFull(sid, hash, InvariantsModeAuto) {
		t.Error("second call with same hash: ShouldEmitFull should return false")
	}
}

// TestShouldEmitFull_AutoHashChangeTriggersFull verifies that when the
// invariants block changes (hash differs from cache), ShouldEmitFull returns
// true again.
func TestShouldEmitFull_AutoHashChangeTriggersFull(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BROWZER_HOME", dir)

	sid := "test-session-change"
	hash1 := HashInvariants("old invariants")
	hash2 := HashInvariants("new invariants — different content")

	// Record hash1 as seen.
	RecordHash(sid, hash1)

	// Now present hash2 — should trigger full emit.
	if !ShouldEmitFull(sid, hash2, InvariantsModeAuto) {
		t.Error("changed hash: ShouldEmitFull should return true")
	}
}

// TestRecordHash_WritesFile verifies that RecordHash creates the cache file.
func TestRecordHash_WritesFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BROWZER_HOME", dir)

	sid := "test-write"
	hash := HashInvariants("block text")
	RecordHash(sid, hash)

	p := filepath.Join(dir, "session", sid+".invariants.sha256")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("cache file not written: %v", err)
	}
	if strings.TrimSpace(string(data)) != hash {
		t.Errorf("cached hash mismatch: want %q got %q", hash, strings.TrimSpace(string(data)))
	}
}

// TestHashMarker_Format verifies the marker string format.
func TestHashMarker_Format(t *testing.T) {
	hash := "abc123"
	got := HashMarker(hash)
	want := "[invariants unchanged — sha256:abc123]"
	if got != want {
		t.Errorf("HashMarker: want %q got %q", want, got)
	}
}

// TestParseInvariantsMode_Values verifies mode string parsing.
func TestParseInvariantsMode_Values(t *testing.T) {
	cases := []struct {
		input string
		want  InvariantsMode
	}{
		{"full", InvariantsModeFull},
		{"FULL", InvariantsModeFull},
		{"hash", InvariantsModeHash},
		{"Hash", InvariantsModeHash},
		{"skip", InvariantsModeSkip},
		{"SKIP", InvariantsModeSkip},
		{"auto", InvariantsModeAuto},
		{"", InvariantsModeAuto},
		{"unknown", InvariantsModeAuto},
	}
	for _, tc := range cases {
		got := ParseInvariantsMode(tc.input)
		if got != tc.want {
			t.Errorf("ParseInvariantsMode(%q) = %q; want %q", tc.input, got, tc.want)
		}
	}
}

// TestSessionID_PPIDFallback verifies that when BROWZER_SESSION_ID is unset
// the function returns a "ppid-<N>" style ID.
func TestSessionID_PPIDFallback(t *testing.T) {
	t.Setenv("BROWZER_SESSION_ID", "")
	id := SessionID()
	if !strings.HasPrefix(id, "ppid-") {
		t.Errorf("expected ppid-<N> fallback; got %q", id)
	}
}

// TestSessionID_EnvOverride verifies that BROWZER_SESSION_ID takes priority.
func TestSessionID_EnvOverride(t *testing.T) {
	t.Setenv("BROWZER_SESSION_ID", "my-session-123")
	id := SessionID()
	if id != "my-session-123" {
		t.Errorf("expected %q; got %q", "my-session-123", id)
	}
}
