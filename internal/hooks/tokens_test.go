package hooks

import (
	"reflect"
	"testing"
)

func TestTokensOf_Empty(t *testing.T) {
	got := TokensOf("")
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %v", got)
	}
}

func TestTokensOf_PlainAscii(t *testing.T) {
	got := TokensOf("the quick brown fox")
	want := []string{"the", "quick", "brown", "fox"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TokensOf ascii: got %v; want %v", got, want)
	}
}

func TestTokensOf_PunctuationAndSymbols(t *testing.T) {
	got := TokensOf("hello, world! foo-bar?baz")
	want := []string{"hello", "world", "foo", "bar", "baz"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("punct split: got %v; want %v", got, want)
	}
}

func TestTokensOf_RetainsDigits(t *testing.T) {
	got := TokensOf("abc123 def456")
	want := []string{"abc123", "def456"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("digits: got %v; want %v", got, want)
	}
}

func TestTokensOf_UnicodeLetters(t *testing.T) {
	got := TokensOf("café naïve Δχ")
	want := []string{"café", "naïve", "Δχ"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unicode: got %v; want %v", got, want)
	}
}

func TestTokensOf_LeadingAndTrailingSeparators(t *testing.T) {
	got := TokensOf("---foo--bar---")
	want := []string{"foo", "bar"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("trim separators: got %v; want %v", got, want)
	}
}

func TestTokensOf_OnlySeparators(t *testing.T) {
	got := TokensOf("---,. !!")
	if len(got) != 0 {
		t.Fatalf("all-separator input should produce empty slice, got %v", got)
	}
}
