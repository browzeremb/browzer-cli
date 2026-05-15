package hooks

import (
	"strings"
	"unicode"
)

// TokensOf returns the input split on every run of non-letter / non-digit
// runes — Unicode-aware. The result is the bag-of-tokens used by the
// prompt-guard vocab lookup path. Empty tokens are dropped.
//
// Mirrors the JS reference at packages/skills/hooks/guards/_util.mjs but
// upgrades the byte-level word splitter to honour Unicode letter and
// digit categories so non-ASCII identifiers (e.g. "café", "naïve",
// "Δχ") tokenise correctly.
func TokensOf(s string) []string {
	if s == "" {
		return []string{}
	}
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if parts == nil {
		return []string{}
	}
	return parts
}
