package runfilters

import (
	"strings"
	"testing"
)

const vitestPassingOnly = `
 ✓ src/foo.test.ts (3 tests | 3 passed) 145ms
 ✓ computes sum (2ms)
 ✓ returns empty list (1ms)
 ✓ handles null (0ms)

Test Files  3 passed (3)
Tests  9 passed (9)
Duration  1.23s
`

const vitestMixed = `
 ✓ src/foo.test.ts (2 tests | 1 passed) 120ms
 ✓ computes sum (2ms)
 × returns empty list

 FAIL src/bar.test.ts

  AssertionError: expected [] to equal [1]
  Expected: [1]
  Received: []

Test Files  1 failed | 2 passed (3)
Tests  1 failed | 8 passed (9)
Duration  2.10s
`

func TestFilterVitest_PassingOnly_JustSummary(t *testing.T) {
	out := filterVitest([]byte(vitestPassingOnly))
	s := string(out)

	// Summary lines must be kept.
	if !strings.Contains(s, "Test Files") {
		t.Error("expected 'Test Files' summary line to be kept")
	}
	if !strings.Contains(s, "Tests") {
		t.Error("expected 'Tests' summary line to be kept")
	}
	if !strings.Contains(s, "Duration") {
		t.Error("expected 'Duration' summary line to be kept")
	}

	// Passing test names must be dropped.
	if strings.Contains(s, "computes sum") {
		t.Error("expected passing test 'computes sum' to be dropped")
	}
	if strings.Contains(s, "returns empty list") && !strings.Contains(vitestMixed, "returns empty list") {
		t.Error("unexpected passing test line retained")
	}
}

func TestFilterVitest_Mixed_FailuresAndSummary(t *testing.T) {
	out := filterVitest([]byte(vitestMixed))
	s := string(out)

	// Failing test name must be kept (× prefix).
	if !strings.Contains(s, "returns empty list") {
		t.Error("expected failing test 'returns empty list' to be kept")
	}

	// FAIL file line must be kept.
	if !strings.Contains(s, "FAIL src/bar.test.ts") {
		t.Error("expected 'FAIL src/bar.test.ts' to be kept")
	}

	// Error messages must be kept.
	if !strings.Contains(s, "AssertionError") {
		t.Error("expected 'AssertionError' to be kept")
	}
	if !strings.Contains(s, "Expected:") {
		t.Error("expected 'Expected:' line to be kept")
	}
	if !strings.Contains(s, "Received:") {
		t.Error("expected 'Received:' line to be kept")
	}

	// Summary must be kept.
	if !strings.Contains(s, "Test Files") {
		t.Error("expected 'Test Files' summary line to be kept")
	}

	// Passing test must be dropped.
	if strings.Contains(s, "computes sum") {
		t.Error("expected passing test 'computes sum' to be dropped")
	}
}

func TestFilterVitest_SkippedLines_Dropped(t *testing.T) {
	// ↓ prefix marks skipped tests in vitest output — must be dropped (FR-01 fix).
	input := " ↓ src/skipped.test.ts (2 tests | 2 skipped)\n" +
		" ↓ handles edge case (0ms)\n" +
		"Test Files  0 failed (1)\n"
	out := filterVitest([]byte(input))
	s := string(out)

	if strings.Contains(s, "↓") {
		t.Error("expected skipped (↓) lines to be dropped")
	}
	if !strings.Contains(s, "Test Files") {
		t.Error("expected summary line to be kept")
	}
}

func TestKeepVitestLine_SkippedReturnsFalse(t *testing.T) {
	// keepVitestLine must return false for ↓-prefixed lines (FR-01 fix).
	if keepVitestLine(" ↓ skipped test") {
		t.Error("keepVitestLine should return false for ↓ prefix")
	}
}

func TestKeepVitestLine_PassReturnsFalse(t *testing.T) {
	if keepVitestLine(" ✓ passing test") {
		t.Error("keepVitestLine should return false for ✓ prefix")
	}
}

func TestFilterVitest_RegistrationKeys(t *testing.T) {
	for _, key := range []string{"vitest", "npx vitest"} {
		args := strings.Fields(key)
		if fn := DefaultRegistry.Resolve(args); fn == nil {
			t.Errorf("expected filter registered for %q", key)
		}
	}
}
