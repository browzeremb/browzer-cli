// Package format provides display-formatting helpers for CLI output.
package format

import (
	"fmt"
	"math"
)

// FormattedScore holds both the normalized score (in [0, 1]) and a human-readable
// label that preserves the original raw value for display alongside the normalized one.
type FormattedScore struct {
	// NormalizedScore is always in [0.0, 1.0]. It is derived from the raw score via
	// a monotone mapping so that rank ordering is preserved across any input range
	// (RRF scores near 0.016 and weighted-graph scores up to ~25+ are both handled).
	NormalizedScore float64

	// RawScoreLabel is a human-readable string representation of the original raw
	// score, e.g. "raw=0.0164". Callers can render it alongside NormalizedScore to
	// show both the absolute and relative relevance of a result.
	RawScoreLabel string
}

// FormatScore maps a raw relevance score (any non-negative float64) to a
// FormattedScore whose NormalizedScore is in [0.0, 1.0].
//
// The mapping uses (2/π)·arctan(raw) which is:
//   - Monotonically increasing — rank order is fully preserved.
//   - Defined for all non-negative inputs with no division-by-zero risk.
//   - Smooth: small RRF scores (~0.016) and large explore scores (~13–25) both
//     produce well-spread values in [0, 1] without needing a batch min/max.
//
// For negative inputs the result is clamped to 0.0 (scores below zero are
// treated as "no relevance").
func FormatScore(raw float64) FormattedScore {
	var normalized float64
	if raw <= 0.0 {
		normalized = 0.0
	} else {
		// (2/π)·arctan(x) maps (0, ∞) → (0, 1), monotonically.
		normalized = (2.0 / math.Pi) * math.Atan(raw)
	}

	return FormattedScore{
		NormalizedScore: normalized,
		RawScoreLabel:   fmt.Sprintf("raw=%.6f", raw),
	}
}
