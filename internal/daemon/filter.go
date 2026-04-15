package daemon

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// nonCodeExt is the set of extensions that always resolve to "none".
var nonCodeExt = map[string]bool{
	".md": true, ".mdx": true, ".json": true, ".yaml": true, ".yml": true,
	".toml": true, ".lock": true, ".env": true, ".gitignore": true,
	".editorconfig": true, ".prettierrc": true,
}

// supportedAggressiveLang is the v1 set of languages that have full manifest
// coverage (server-side AST). Other languages fall through to "minimal".
var supportedAggressiveLang = map[string]bool{
	"typescript": true, "javascript": true, "go": true, "python": true,
}

// ResolveAuto picks a concrete filter level for a given path/manifest entry.
// Spec §4.2.
func ResolveAuto(path string, mf ManifestFile) string {
	if nonCodeExt[strings.ToLower(filepath.Ext(path))] {
		return "none"
	}
	if !supportedAggressiveLang[mf.Language] {
		return "minimal"
	}
	if mf.LineCount <= 300 {
		return "minimal"
	}
	return "aggressive"
}

// ApplyFilter applies the requested filter level. Returns the filtered
// bytes and the effective level used (resolves "auto" to a concrete level).
func ApplyFilter(in []byte, level string, mf ManifestFile) ([]byte, string) {
	if level == "" || level == "auto" {
		level = ResolveAuto("", mf)
	}
	switch level {
	case "none":
		return in, "none"
	case "minimal":
		return stripComments(in, mf.Language), "minimal"
	case "aggressive":
		out, ok := keepSignatures(in, mf)
		if !ok {
			return stripComments(in, mf.Language), "minimal"
		}
		return out, "aggressive"
	default:
		return in, "none"
	}
}

var (
	reLineCommentSlash = regexp.MustCompile(`(?m)\s*//.*$`)
	reLineCommentHash  = regexp.MustCompile(`(?m)\s*#.*$`)
	reBlockComment     = regexp.MustCompile(`(?s)/\*.*?\*/`)
	reBlankRuns        = regexp.MustCompile(`\n{3,}`)
)

func stripComments(in []byte, language string) []byte {
	out := in
	switch language {
	case "python":
		out = reLineCommentHash.ReplaceAll(out, nil)
	default:
		out = reBlockComment.ReplaceAll(out, nil)
		out = reLineCommentSlash.ReplaceAll(out, nil)
	}
	out = reBlankRuns.ReplaceAll(out, []byte("\n\n"))
	return out
}

// keepSignatures rebuilds the file to contain only:
//   - the imports (verbatim, lines outside any symbol range)
//   - each symbol's signature line + its docstring lines
//
// Returns ok=false when the manifest has no symbols (caller falls back).
func keepSignatures(in []byte, mf ManifestFile) ([]byte, bool) {
	if len(mf.Symbols) == 0 {
		return nil, false
	}
	lines := strings.Split(string(in), "\n")
	// 1. Sort symbols by StartLine for deterministic output.
	syms := make([]ManifestSymbol, len(mf.Symbols))
	copy(syms, mf.Symbols)
	sort.Slice(syms, func(i, j int) bool { return syms[i].StartLine < syms[j].StartLine })

	inSymbol := make([]bool, len(lines)+1) // 1-indexed
	for _, s := range syms {
		for i := s.StartLine; i <= s.EndLine && i-1 < len(lines); i++ {
			inSymbol[i] = true
		}
	}

	var b strings.Builder
	// Pass 1: top-of-file imports (lines before the first symbol).
	if syms[0].StartLine > 1 {
		for i := 0; i < syms[0].StartLine-1 && i < len(lines); i++ {
			b.WriteString(lines[i])
			b.WriteByte('\n')
		}
	}
	// Pass 2: per-symbol signature + docstring.
	for _, s := range syms {
		if s.Doc != "" {
			b.WriteString("/** ")
			b.WriteString(s.Doc)
			b.WriteString(" */\n")
		}
		if s.Signature != "" {
			b.WriteString(s.Signature)
		} else if s.StartLine-1 < len(lines) {
			b.WriteString(lines[s.StartLine-1])
		}
		b.WriteString(" { /* ... body stripped */ }\n\n")
	}
	return []byte(b.String()), true
}
