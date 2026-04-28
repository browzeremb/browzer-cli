// Package format — score normalization tests (T-02-08).
//
// RED: FormatScore and FormattedScore do not exist yet.
// Expected failure: "undefined: FormatScore" / "undefined: FormattedScore".
package format

import (
	"math"
	"strings"
	"testing"
)

// TestFormatScoreRange verifies that FormatScore maps any raw score value
// (RRF ~0.016 range and explore weighted-graph ~13.5 range) to a string
// representation whose parsed float is in [0.0, 1.0].
func TestFormatScoreRange(t *testing.T) {
	cases := []struct {
		name     string
		rawScore float64
	}{
		{"rrf_min", 0.016393442622950818},
		{"rrf_typical", 0.016129032258064516},
		{"rrf_lower", 0.015873015873015872},
		{"mid_range", 1.0},
		{"explore_large", 13.5},
		{"explore_very_large", 25.0},
		{"zero", 0.0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := FormatScore(tc.rawScore)
			// FormatScore must return a FormattedScore whose NormalizedScore is in [0,1].
			if result.NormalizedScore < 0.0 || result.NormalizedScore > 1.0 {
				t.Errorf("FormatScore(%v).NormalizedScore = %v; want in [0.0, 1.0]",
					tc.rawScore, result.NormalizedScore)
			}
		})
	}
}

// TestFormatScoreRawScoreLabel verifies that FormatScore preserves the original
// raw score in the RawScoreLabel field so callers can show the un-normalized
// value alongside the normalized one.
func TestFormatScoreRawScoreLabel(t *testing.T) {
	cases := []struct {
		rawScore float64
	}{
		{0.016393442622950818},
		{13.5},
	}

	for _, tc := range cases {
		result := FormatScore(tc.rawScore)
		// RawScoreLabel must be non-empty and contain a numeric representation
		// of the raw score.
		if result.RawScoreLabel == "" {
			t.Errorf("FormatScore(%v).RawScoreLabel is empty; want a numeric label", tc.rawScore)
		}
		// Sanity: the label should contain at least one digit.
		hasDigit := false
		for _, ch := range result.RawScoreLabel {
			if ch >= '0' && ch <= '9' {
				hasDigit = true
				break
			}
		}
		if !hasDigit {
			t.Errorf("FormatScore(%v).RawScoreLabel = %q; expected to contain digits",
				tc.rawScore, result.RawScoreLabel)
		}
	}
}

// TestFormatScoreRankPreserving verifies that FormatScore is strictly monotone
// over the full range of raw scores exercised by TestFormatScoreRange.
//
// Unlike the former two-constant spot check (which a special-cased
// implementation could pass), this iterates every consecutive pair from the
// range table in ascending order and asserts strict monotonicity — ensuring
// the arctan transform cannot be broken while still satisfying the two
// hardcoded values.
func TestFormatScoreRankPreserving(t *testing.T) {
	// All distinct positive values from TestFormatScoreRange, sorted ascending.
	// Zero is excluded because arctan(0) == 0, same as the floor, and we want
	// strict > comparisons between positive inputs.
	// rrf_lower = 1/63 ≈ 0.01587 < rrf_typical = 1/62 ≈ 0.01613 < rrf_min = 1/61 ≈ 0.01639
	ascending := []float64{
		0.015873015873015872, // rrf_lower ≈ 1/63
		1.0 / (60 + 2),      // rrf_typical ≈ 1/62 = 0.01613
		1.0 / (60 + 1),      // rrf_min    ≈ 1/61 = 0.01639
		1.0,
		13.5,
		25.0,
	}

	for i := 1; i < len(ascending); i++ {
		a := ascending[i-1]
		b := ascending[i]
		fa := FormatScore(a)
		fb := FormatScore(b)
		if fb.NormalizedScore <= fa.NormalizedScore {
			t.Errorf("monotonicity violation at index %d: FormatScore(%v).NormalizedScore=%v >= FormatScore(%v).NormalizedScore=%v",
				i, b, fb.NormalizedScore, a, fa.NormalizedScore)
		}
	}
}

// TestFormatScoreIdenticalInputs verifies that equal raw scores produce equal
// NormalizedScore values (no NaN via division by zero when min==max).
//
// FormatScore normalizes a single value independently (using a known global
// range or clamping), so calling it twice with the same value must be stable.
func TestFormatScoreIdenticalInputs(t *testing.T) {
	a := FormatScore(0.5)
	b := FormatScore(0.5)

	if a.NormalizedScore != b.NormalizedScore {
		t.Errorf("identical inputs produced different normalized scores: %v vs %v",
			a.NormalizedScore, b.NormalizedScore)
	}
	if strings.Contains("NaN", a.RawScoreLabel) {
		t.Errorf("RawScoreLabel contains NaN: %q", a.RawScoreLabel)
	}
}

// TestFormatScoreParityWithTS verifies that the Go formula and the TypeScript
// formula (packages/core/src/search/score-normalize.ts) return the same value
// for a shared sample rawScore=0.5.
//
// Expected: (2/π)·atan(0.5) ≈ 0.2951672353008666
//
// This test ALSO guards the single-result path: if Go were to return 1.0 for a
// single input (the old min-max artifact), this assertion would fail immediately
// since 0.295 ≠ 1.0.
//
// Mirrored in packages/core/src/__tests__/contract/score-normalize-contract.test.ts
// ("cross-layer parity" test case) to make drift between TS and Go visible in CI.
func TestFormatScoreParityWithTS(t *testing.T) {
	const rawScore = 0.5

	result := FormatScore(rawScore)

	// Compute the expected value using the same formula in Go: (2/π)·atan(0.5) ≈ 0.2952
	want := (2.0 / math.Pi) * math.Atan(rawScore)

	const epsilon = 1e-10
	if diff := math.Abs(result.NormalizedScore - want); diff > epsilon {
		t.Errorf("FormatScore(%v).NormalizedScore = %.15f; want %.15f (diff=%e)",
			rawScore, result.NormalizedScore, want, diff)
	}

	// Explicit not-1.0 guard: the single-result path must NOT return 1.0.
	if math.Abs(result.NormalizedScore-1.0) < 0.01 {
		t.Errorf("FormatScore(%v).NormalizedScore ≈ 1.0 (%v); single-result must use arctan, not 1.0",
			rawScore, result.NormalizedScore)
	}

	// Cross-check: the result must be in [0.28, 0.31] for rawScore=0.5
	// so that any gross implementation error (e.g. returning raw directly) fails.
	if result.NormalizedScore < 0.28 || result.NormalizedScore > 0.31 {
		t.Errorf("FormatScore(%v).NormalizedScore = %v; expected approx 0.295 (range [0.28, 0.31])",
			rawScore, result.NormalizedScore)
	}

}
