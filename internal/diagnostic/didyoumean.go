// Package diagnostic provides utilities for producing human-friendly error
// messages from CUE schema violations.
//
// It is intentionally free of CUE imports so it can be unit-tested without
// triggering the CUE compilation overhead and without coupling to the schema
// package. The diagnostic package consumes []string slices of field/value
// names and produces suggestion strings; callers in the schema and commands
// packages bridge between the CUE layer and this package.
package diagnostic

import (
	"sort"
	"strings"
)

// DidYouMean returns a list of candidates from `pool` that are "close" to
// `input` (Levenshtein distance ≤ 2 OR exact-prefix match on either side).
// Results are sorted by (distance ascending, candidate ascending) so the
// best suggestion is always first.
//
// Returns nil when no candidate qualifies (empty when distance threshold
// would produce too many noisy suggestions).
//
// The pool is expected to be ≤ a few hundred items (schema field lists).
// The algorithm is O(|input| · |candidate|) per pair — acceptable for
// interactive error rendering where N is small.
func DidYouMean(input string, pool []string) []string {
	if input == "" || len(pool) == 0 {
		return nil
	}
	inputLow := strings.ToLower(input)

	type ranked struct {
		name string
		dist int
	}
	var hits []ranked

	for _, candidate := range pool {
		if candidate == "" {
			continue
		}
		candidateLow := strings.ToLower(candidate)
		dist := levenshtein(inputLow, candidateLow)
		isPrefixMatch := strings.HasPrefix(inputLow, candidateLow) ||
			strings.HasPrefix(candidateLow, inputLow)
		if dist <= 2 || isPrefixMatch {
			hits = append(hits, ranked{name: candidate, dist: dist})
		}
	}

	if len(hits) == 0 {
		return nil
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].dist != hits[j].dist {
			return hits[i].dist < hits[j].dist
		}
		return hits[i].name < hits[j].name
	})

	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.name
	}
	return out
}

// levenshtein computes the edit distance between two strings.
// Uses the standard DP matrix approach; handles empty strings correctly.
func levenshtein(a, b string) int {
	ra := []rune(a)
	rb := []rune(b)
	la, lb := len(ra), len(rb)

	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	// Two-row DP — O(la·lb) time, O(lb) space.
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)

	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			insert := curr[j-1] + 1
			delete := prev[j] + 1
			replace := prev[j-1] + cost
			curr[j] = min(insert, delete, replace)
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

// FormatSuggestion returns a human-readable "did you mean: X, Y?" string for
// a list of candidates. Returns empty string when candidates is nil/empty.
// Caps at 3 suggestions to keep the message compact.
func FormatSuggestion(candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}
	top := candidates
	if len(top) > 3 {
		top = top[:3]
	}
	return "did you mean: " + strings.Join(top, ", ") + "?"
}
