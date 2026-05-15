package hooks

import (
	"regexp"
	"strings"
)

// heredocHeaderRE matches the opening of a heredoc redirection:
//
//	<<DELIM       — quoted-literal disabled, single token after <<
//	<<-DELIM      — same, with leading-tab stripping
//	<<'DELIM'     — quoted body, no expansion
//	<<"DELIM"     — quoted body, no expansion
//
// Capture groups: 1=single-quoted DELIM, 2=double-quoted DELIM, 3=bare DELIM.
var heredocHeaderRE = regexp.MustCompile(`^<<-?\s*(?:'([^'\n]+)'|"([^"\n]+)"|([A-Za-z_][\w]*))`)

// StripQuoted returns the input string with single-quoted, double-quoted,
// $(...) command substitutions, and <<DELIM heredoc bodies removed —
// leaving only the "shell skeleton" the parent shell interprets as command
// tokens.
//
// Use this before substring matching on a Bash invocation. Naive grep on
// the raw command line surfaces tokens that live inside argument bodies —
// notably `git commit -m "$(cat <<'EOF' ... browzer explore ... EOF)"`,
// which must NOT classify as a `browzer explore` call.
//
// Not a full POSIX parser; handles the cases that bite the hook surface:
//   - single-quoted strings (no escapes inside)
//   - double-quoted strings (backslash escapes recognised)
//   - $(...) command substitution (depth-aware paren balancing)
//   - <<DELIM / <<-DELIM / <<'DELIM' / <<"DELIM" heredocs
//   - ANSI-C $'...' treated as single-quoted (good enough)
//
// Unterminated regions return the skeleton accumulated so far — i.e.
// dangling quotes/heredocs swallow the remainder of the input.
func StripQuoted(input string) string {
	if input == "" {
		return ""
	}
	var out strings.Builder
	out.Grow(len(input))
	i := 0
	n := len(input)
	for i < n {
		c := input[i]

		// Single-quoted: literal until next ' (no escapes).
		if c == '\'' {
			end := strings.IndexByte(input[i+1:], '\'')
			if end < 0 {
				return out.String()
			}
			i = i + 1 + end + 1
			continue
		}

		// ANSI-C $'...' — same termination rule as single-quoted.
		if c == '$' && i+1 < n && input[i+1] == '\'' {
			end := strings.IndexByte(input[i+2:], '\'')
			if end < 0 {
				return out.String()
			}
			i = i + 2 + end + 1
			continue
		}

		// Double-quoted: skip until next unescaped ". Within the body
		// we must still skip $(...) command substitutions and heredocs
		// that open *inside* the quoted span — otherwise the inner
		// `"..."` of a $() argument closes the outer quote prematurely
		// (the heredoc-inside-$()-inside-"" pattern from
		// `git commit -m "$(cat <<'EOF' ... \"TOKEN\" ... EOF)"`).
		if c == '"' {
			j := i + 1
			for j < n {
				if input[j] == '\\' && j+1 < n {
					j += 2
					continue
				}
				if input[j] == '"' {
					break
				}
				// Nested $(...) — depth-aware paren balance,
				// recursing into heredocs along the way.
				if input[j] == '$' && j+1 < n && input[j+1] == '(' {
					j = skipDollarParen(input, j)
					continue
				}
				// Nested heredoc — skip its body so quoted tokens
				// inside don't leak as "" terminators.
				if input[j] == '<' && j+1 < n && input[j+1] == '<' {
					if end, ok := skipHeredoc(input, j); ok {
						_, _ = j, end

					}
				}
				j++
			}
			if j >= n {
				// Unterminated — drop the rest.
				return out.String()
			}
			i = j + 1
			continue
		}

		// $(...) command substitution: skip nested parens (depth-aware).
		if c == '$' && i+1 < n && input[i+1] == '(' {
			i = skipDollarParen(input, i)
			continue
		}

		// Heredoc opened at the shell skeleton level.
		if c == '<' && i+1 < n && input[i+1] == '<' {
			if end, ok := skipHeredoc(input, i); ok {
				i = end
				continue
			}
			// Header parsed but body unterminated — keep header in skeleton.
			rest := input[i:]
			if m := heredocHeaderRE.FindStringSubmatch(rest); m != nil {
				headerEnd := i + len(m[0])
				out.WriteString(input[i:headerEnd])
				return out.String()
			}
		}

		out.WriteByte(c)
		i++
	}
	return out.String()
}

// skipDollarParen consumes a `$(...)` substitution that opens at offset
// `start` (where input[start] == '$' and input[start+1] == '('). Returns
// the offset immediately after the matching `)`. When the substitution is
// unterminated, returns len(input) so the caller drops the remainder.
//
// Recurses-by-iteration into nested $(...) and heredocs so a `)` that
// lives inside a heredoc body cannot close the outer substitution.
func skipDollarParen(input string, start int) int {
	n := len(input)
	depth := 1
	j := start + 2
	for j < n && depth > 0 {
		if input[j] == '<' && j+1 < n && input[j+1] == '<' {
			if end, ok := skipHeredoc(input, j); ok {
				j = end
				continue
			}
			// Unterminated heredoc inside $() — abandon scan.
			return n
		}
		if input[j] == '$' && j+1 < n && input[j+1] == '(' {
			j = skipDollarParen(input, j)
			continue
		}
		switch input[j] {
		case '(':
			depth++
		case ')':
			depth--
		}
		j++
	}
	return j
}

// skipHeredoc consumes a heredoc header + body opening at offset `start`
// (where input[start] == '<' and input[start+1] == '<'). Returns the
// offset immediately after the closing DELIM line and ok=true on
// success. ok=false means the bytes at `start` don't look like a
// heredoc header at all (caller should fall back to byte-level scan).
// A header that parses but whose body never terminates is treated as
// a successful consumption that swallows everything to EOF — caller
// receives ok=true and end=len(input).
func skipHeredoc(input string, start int) (int, bool) {
	rest := input[start:]
	m := heredocHeaderRE.FindStringSubmatch(rest)
	if m == nil {
		return 0, false
	}
	delim := m[3]
	if delim == "" {
		delim = m[1]
	}
	if delim == "" {
		delim = m[2]
	}
	headerEnd := start + len(m[0])
	pattern := `\n[\t ]*` + regexp.QuoteMeta(delim) + `(?:\n|$)`
	re, err := regexp.Compile(pattern)
	if err != nil {
		return len(input), true
	}
	loc := re.FindStringIndex(input[headerEnd:])
	if loc == nil {
		// Unterminated body — swallow to EOF.
		return len(input), true
	}
	return headerEnd + loc[1], true
}
