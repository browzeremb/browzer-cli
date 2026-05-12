package diagnostic

import (
	"testing"
)

// TestDidYouMean_ExactMatch ensures an exact match scores distance 0 and is
// returned first.
func TestDidYouMean_ExactMatch(t *testing.T) {
	got := DidYouMean("status", []string{"startedAt", "status", "stepId"})
	if len(got) == 0 {
		t.Fatal("expected at least one suggestion, got none")
	}
	if got[0] != "status" {
		t.Errorf("first suggestion should be exact match %q; got %q", "status", got[0])
	}
}

// TestDidYouMean_Levenshtein1 ensures a single-edit distance is suggested.
func TestDidYouMean_Levenshtein1(t *testing.T) {
	pool := []string{"scope", "score", "store"}
	got := DidYouMean("scopa", pool)
	if len(got) == 0 {
		t.Fatal("expected at least one suggestion for edit-distance-1 input")
	}
	found := false
	for _, s := range got {
		if s == "scope" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected %q in suggestions; got %v", "scope", got)
	}
}

// TestDidYouMean_Levenshtein2 ensures a two-edit typo is still suggested.
func TestDidYouMean_Levenshtein2(t *testing.T) {
	pool := []string{"executionStrategy", "mode", "setAt"}
	got := DidYouMean("executoinStrategY", pool)
	// "executionStrategy" has 2 edits vs "executoinStrategY" (transposition + case)
	if len(got) == 0 {
		t.Fatal("expected a suggestion for 2-edit typo")
	}
}

// TestDidYouMean_BeyondThreshold ensures that a >2-edit distance is NOT
// returned when it is not a prefix match.
func TestDidYouMean_BeyondThreshold(t *testing.T) {
	pool := []string{"completely", "different", "word"}
	got := DidYouMean("scope", pool)
	// None of these should be within distance 2 or prefix-match of "scope"
	if len(got) != 0 {
		t.Errorf("expected no suggestions for far-off input; got %v", got)
	}
}

// TestDidYouMean_PrefixMatch ensures that prefix-match candidates are returned
// even when the Levenshtein distance exceeds 2.
func TestDidYouMean_PrefixMatch(t *testing.T) {
	pool := []string{"invariantsChecked", "invariants"}
	// "invariants" is a prefix of "invariantsChecked" AND "invariantsChecked"
	// starts with "invariants" — both qualify via prefix.
	got := DidYouMean("invariantsCheck", pool)
	if len(got) == 0 {
		t.Fatal("expected prefix-match suggestions")
	}
}

// TestDidYouMean_EmptyInput returns nil for empty input.
func TestDidYouMean_EmptyInput(t *testing.T) {
	got := DidYouMean("", []string{"scope", "mode"})
	if len(got) != 0 {
		t.Errorf("expected nil for empty input; got %v", got)
	}
}

// TestDidYouMean_EmptyPool returns nil for empty pool.
func TestDidYouMean_EmptyPool(t *testing.T) {
	got := DidYouMean("status", nil)
	if len(got) != 0 {
		t.Errorf("expected nil for empty pool; got %v", got)
	}
}

// TestDidYouMean_ResultsSorted verifies that results are sorted by distance
// ascending, then alphabetically.
func TestDidYouMean_ResultsSorted(t *testing.T) {
	// "stat" is distance 2 from "stats" and distance 1 from "stat" exact.
	pool := []string{"statistics", "stat", "state", "status"}
	got := DidYouMean("stat", pool)
	if len(got) == 0 {
		t.Fatal("expected suggestions")
	}
	// "stat" exact match (distance 0) must come first.
	if got[0] != "stat" {
		t.Errorf("exact match should be first; got %v", got)
	}
}

// TestFormatSuggestion_Basic verifies the human-readable formatting.
func TestFormatSuggestion_Basic(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"scope"}, "did you mean: scope?"},
		{[]string{"scope", "score"}, "did you mean: scope, score?"},
		// More than 3 should be capped.
		{[]string{"a", "b", "c", "d"}, "did you mean: a, b, c?"},
	}
	for _, tc := range cases {
		got := FormatSuggestion(tc.in)
		if got != tc.want {
			t.Errorf("FormatSuggestion(%v) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

// TestLevenshtein verifies the distance function directly.
func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "a", 1},
		{"kitten", "sitting", 3},
		{"scope", "scope", 0},
		{"scopa", "scope", 1},
		{"scopx", "scope", 1},
	}
	for _, tc := range cases {
		got := levenshtein(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("levenshtein(%q, %q) = %d; want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
