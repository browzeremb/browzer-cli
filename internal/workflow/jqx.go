package workflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/itchyny/gojq"
)

// marshalNoHTMLEscape returns a compact JSON encoding of v without escaping
// `<`, `>`, or `&`. Mirrors `json.Marshal` semantics (no indent, no trailing
// newline) but disables the HTML-safety pass that the stdlib applies by
// default. Used by `GetField` so `--field` output matches what `jq -c` would
// produce — operators reading PRD acceptance criteria like `WHEN x > y` don't
// see the surprising `>` form.
func marshalNoHTMLEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// Encoder always appends '\n'; trim to match json.Marshal semantics.
	out := buf.Bytes()
	if n := len(out); n > 0 && out[n-1] == '\n' {
		out = out[:n-1]
	}
	return out, nil
}

// pathSegment is one resolved hop on a GetField path: either an object key
// lookup ("foo") or an array index lookup ("[3]" / "[-1]"). The two forms are
// mutually exclusive — exactly one of key/index is meaningful per segment.
type pathSegment struct {
	isIndex bool
	key     string // when !isIndex
	index   int    // when isIndex; may be negative
}

// parseFieldPath splits a GetField path string into a sequence of segments,
// supporting both dot-separated object keys ("config.mode") and bracketed
// array indices ("steps[0]", "steps[-1]", "task.execution.agents[-1].notes").
//
// Negative indices match gojq semantics: ".arr[-1]" is the last element,
// ".arr[-2]" is the second-to-last, etc. Out-of-range indices (positive or
// negative) are not rejected at parse time — resolution returns null
// downstream, mirroring jq.
//
// Errors are returned only for genuinely malformed paths: unclosed `[`,
// empty `[]`, non-integer index ("[a]"), or `..` (empty segment).
//
// F-6: this is INTENTIONALLY a restricted dialect — only dot-separated
// keys and bracketed integer indices are supported. Quoted keys
// (`["foo bar"]`), array slices (`[2:5]`), pipes, filters, and any other
// gojq construct are NOT recognised and are rejected as malformed paths.
// We deliberately do not delegate to gojq.Parse here: the surface this
// verb exposes via `--field` is contractually narrower than full jq, and
// keeping the parser hand-rolled pins that contract. Callers that need
// the full dialect should use `--jq` instead.
func parseFieldPath(path string) ([]pathSegment, error) {
	if path == "" {
		return nil, fmt.Errorf("empty path")
	}
	var segs []pathSegment
	var cur strings.Builder
	// F-11: flushKey has no failure mode; previously it returned an unused
	// error sentinel that inflated branch count at every call site.
	flushKey := func() {
		s := cur.String()
		cur.Reset()
		if s == "" {
			return
		}
		segs = append(segs, pathSegment{key: s})
	}
	runes := []rune(path)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		switch ch {
		case '.':
			flushKey()
		case '[':
			// Flush any pending key segment before consuming the index.
			flushKey()
			// Find matching ']'.
			end := -1
			for j := i + 1; j < len(runes); j++ {
				if runes[j] == ']' {
					end = j
					break
				}
			}
			if end == -1 {
				return nil, fmt.Errorf("unclosed '[' in path %q", path)
			}
			body := string(runes[i+1 : end])
			if body == "" {
				return nil, fmt.Errorf("empty '[]' in path %q", path)
			}
			n, err := strconv.Atoi(body)
			if err != nil {
				return nil, fmt.Errorf("invalid array index %q in path %q", body, path)
			}
			segs = append(segs, pathSegment{isIndex: true, index: n})
			i = end
		case ']':
			return nil, fmt.Errorf("unexpected ']' in path %q", path)
		default:
			cur.WriteRune(ch)
		}
	}
	flushKey()
	if len(segs) == 0 {
		return nil, fmt.Errorf("empty path %q", path)
	}
	return segs, nil
}

// GetField extracts a value from a parsed JSON document (any) using a
// path expression that supports object keys and bracketed array indices.
//
// Path syntax:
//   - "config.mode"                     — nested object key
//   - "steps[0]"                        — positive array index
//   - "steps[-1]"                       — negative array index (last element)
//   - "task.execution.agents[-1].notes" — chained
//
// Return conventions:
//   - Scalar string/number/bool/null values are returned as their plain %v
//     representation (unquoted) when asJSON is false.
//   - When asJSON is true, all values (including scalars) are returned as their
//     JSON encoding.
//   - Objects and arrays are always returned as compact JSON regardless of
//     asJSON.
//   - A missing intermediate key, an out-of-range array index (positive or
//     negative), or indexing into a non-array returns an error consistent
//     with the previous "not found" contract — callers above branch on error,
//     not on null-string.
//   - F-9: a JSON null SCALAR at the resolved path returns the bare string
//     "null" (asJSON=false) or "null" (asJSON=true) — both intentional, both
//     pinned by tests. Indexing INTO a JSON null intermediate (e.g. agents
//     is null) returns an "expected array" error, NOT "null". `[-1]` on a
//     length-0 array is an out-of-range error, NOT "null".
func GetField(data any, path string, asJSON bool) (string, error) {
	segs, err := parseFieldPath(path)
	if err != nil {
		return "", err
	}
	cur := data

	for _, seg := range segs {
		if seg.isIndex {
			arr, ok := cur.([]any)
			if !ok {
				return "", fmt.Errorf("field %q not found: expected array for index [%d], got %T", path, seg.index, cur)
			}
			n := len(arr)
			idx := seg.index
			if idx < 0 {
				idx = n + idx
			}
			if idx < 0 || idx >= n {
				return "", fmt.Errorf("field %q not found: index [%d] out of range (array length %d)", path, seg.index, n)
			}
			cur = arr[idx]
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return "", fmt.Errorf("field %q not found: expected object at %q, got %T", path, seg.key, cur)
		}
		val, exists := m[seg.key]
		if !exists {
			return "", fmt.Errorf("field %q not found in document", path)
		}
		cur = val
	}

	// cur is the resolved value.
	switch v := cur.(type) {
	case map[string]any, []any:
		// Always JSON-encode objects and arrays.
		b, err := marshalNoHTMLEscape(v)
		if err != nil {
			return "", fmt.Errorf("marshal field %q: %w", path, err)
		}
		return string(b), nil
	case nil:
		if asJSON {
			return "null", nil
		}
		return "null", nil
	default:
		if asJSON {
			b, err := marshalNoHTMLEscape(v)
			if err != nil {
				return "", fmt.Errorf("marshal scalar %q: %w", path, err)
			}
			return string(b), nil
		}
		return fmt.Sprintf("%v", v), nil
	}
}

// ApplyJQ applies a jq expression to data (which must be a map[string]any
// decoded from JSON) and returns the mutated value.
// The expression must produce exactly one output value; an expression that
// produces zero or multiple outputs returns an error.
func ApplyJQ(data any, expr string) (any, error) {
	return ApplyJQWithVars(data, expr, nil)
}

// jqTokenizer tracks lexical state while walking a jq program rune-by-rune.
// It handles string literals, line comments, and bracket/brace/paren nesting
// so callers can detect top-level positions without duplicating the state machine.
//
// Usage: call Feed(ch) for each rune (in order); the tokenizer updates its
// internal state. After each call, AtTopLevel() reports whether the rune that
// was just fed sits at depth 0 outside strings and comments.
//
// Design note: this struct is shared by rewriteMultiStatementJQ to keep the
// single-pass state machine in one place (Option A, F-5). The public API of
// ApplyJQ / ApplyJQWithVars is unchanged.
type jqTokenizer struct {
	parenDepth   int
	bracketDepth int
	braceDepth   int
	inString     bool
	inComment    bool
}

// AtTopLevel reports whether the tokenizer's current position is at nesting
// depth 0, outside any string literal or line comment.
func (t *jqTokenizer) AtTopLevel() bool {
	return !t.inString && !t.inComment &&
		t.parenDepth == 0 && t.bracketDepth == 0 && t.braceDepth == 0
}

// Feed processes one rune and updates internal state.
// Returns true if the rune is "visible" (not skipped by escape handling)
// and false when the caller should skip it (never — escape skipping is done
// by the caller via ConsumeEscape; Feed itself always processes exactly one rune).
func (t *jqTokenizer) Feed(ch rune) {
	if !t.inString && ch == '#' {
		t.inComment = true
	}
	if t.inComment {
		if ch == '\n' {
			t.inComment = false
		}
		return
	}
	if t.inString {
		if ch == '"' {
			t.inString = false
		}
		return
	}
	if ch == '"' {
		t.inString = true
		return
	}
	t.updateDepth(ch)
}

// updateDepth adjusts bracket/brace/paren depths for a single rune.
// Called only when not inside a string or comment.
func (t *jqTokenizer) updateDepth(ch rune) {
	switch ch {
	case '(':
		t.parenDepth++
	case ')':
		if t.parenDepth > 0 {
			t.parenDepth--
		}
	case '[':
		t.bracketDepth++
	case ']':
		if t.bracketDepth > 0 {
			t.bracketDepth--
		}
	case '{':
		t.braceDepth++
	case '}':
		if t.braceDepth > 0 {
			t.braceDepth--
		}
	}
}

// rewriteMultiStatementJQ converts standalone-jq-style multi-statement
// programs (`expr1 ; expr2`) to single-pipeline form
// (`(expr1\n) | (expr2\n)`) for gojq compatibility. Single-statement
// programs pass through unchanged (byte-equal to input; no parens added).
//
// Design choice (Option A): a jqTokenizer struct holds the shared state
// machine (paren/bracket/brace depth, string, comment) used by both the
// `def ` bail-out check and the `;` collector in a single pass. This
// replaces the previous two-pass approach (separate containsTopLevelDef
// pre-scan) and distributes the complexity across jqTokenizer methods so
// each function stays ≤15 cyclomatic complexity.
//
// The newline before each closing paren (`s + "\n)"`) ensures that statements
// ending in a line comment (e.g. `.x = 1 # note`) do not swallow the `)` into
// the comment when gojq parses the combined pipeline.
//
// Special case: a top-level `def ` keyword causes verbatim return — gojq
// handles def's internal `;` natively; splitting across a def boundary
// corrupts it into fragments gojq cannot compile.
//
// Edge cases:
//   - Trailing `;` → single-element split → returned verbatim.
//   - Consecutive `;;` or leading `;` → empty tokens skipped.
//   - Single-statement (no top-level `;`) → returned verbatim.
func rewriteMultiStatementJQ(program string) string {
	var tok jqTokenizer
	runes := []rune(program)
	var stmts []string
	var cur strings.Builder

	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		tok.Feed(ch)

		// Inside a string: handle escape sequences. The escaped rune is written
		// verbatim and skipped by the tokenizer — in particular, a `\"` must NOT
		// be fed to tok.Feed because Feed would interpret the `"` as closing the
		// string. The `\\` already left inString=true; the next rune is opaque.
		if tok.inString && ch == '\\' && i+1 < len(runes) {
			cur.WriteRune(ch)
			i++
			cur.WriteRune(runes[i])
			continue
		}

		if tok.AtTopLevel() {
			// Bail out verbatim on top-level `def ` — let gojq parse natively.
			if ch == 'd' && i+4 <= len(runes) && string(runes[i:i+4]) == "def " {
				return program
			}
			// Top-level `;` splits statements.
			if ch == ';' {
				if stmt := strings.TrimSpace(cur.String()); stmt != "" {
					stmts = append(stmts, stmt)
				}
				cur.Reset()
				continue
			}
		}

		cur.WriteRune(ch)
	}
	if tail := strings.TrimSpace(cur.String()); tail != "" {
		stmts = append(stmts, tail)
	}

	if len(stmts) <= 1 {
		return program // Zero or one statement: preserve original whitespace.
	}
	parts := make([]string, len(stmts))
	for i, s := range stmts {
		parts[i] = "(" + s + "\n)"
	}
	return strings.Join(parts, " | ")
}

// ApplyJQWithVars is ApplyJQ extended with bind variables — the gojq
// equivalent of `jq --arg KEY VALUE` / `jq --argjson KEY <json>`. Each
// entry in vars binds `$KEY` inside the expression to the value (which
// must be a JSON-decodable Go value: string for --arg, any for --argjson).
//
// Variable names MUST NOT carry a leading `$`; pass `id`, not `$id`. The
// underlying gojq.WithVariables expects names in `$id` form, so this
// function prepends the sigil.
//
// Variable insertion order is sorted by key name to keep the bind sequence
// deterministic across calls — gojq pairs WithVariables names with the
// values supplied to Run() positionally.
func ApplyJQWithVars(data any, expr string, vars map[string]any) (any, error) {
	expr = rewriteMultiStatementJQ(expr)
	q, err := gojq.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("jq parse: %w", err)
	}

	options := []gojq.CompilerOption{
		// Disable the gojq `env` / `$ENV` builtin to prevent exfiltrating
		// process environment variables (e.g. BROWZER_TOKEN, AWS_ACCESS_KEY_ID)
		// into workflow.json, which is version-controlled. See OWASP F-sec-4.
		gojq.WithEnvironLoader(func() []string { return nil }),
	}

	var bindNames []string
	var values []any
	if len(vars) > 0 {
		keys := make([]string, 0, len(vars))
		for k := range vars {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		bindNames = make([]string, 0, len(keys))
		values = make([]any, 0, len(keys))

		// CRITICAL: WithVariables(bindNames) and Run(data, values...) must be index-aligned.
		// Build bindNames AND values inside the SAME loop iteration — splitting into two passes
		// over the sorted-keys slice would silently break alignment if Go map iteration randomness
		// changed. Future maintainers: do not refactor into multiple passes.
		for _, k := range keys {
			if !isValidJQVarName(k) {
				return nil, fmt.Errorf("invalid jq variable name %q (must match [A-Za-z_][A-Za-z0-9_]*)", k)
			}
			bindNames = append(bindNames, "$"+k)
			values = append(values, vars[k])
		}
		options = append(options, gojq.WithVariables(bindNames))
	}

	code, err := gojq.Compile(q, options...)
	if err != nil {
		return nil, fmt.Errorf("jq compile: %w", err)
	}

	iter := code.Run(data, values...)
	v, ok := iter.Next()
	if !ok {
		return nil, fmt.Errorf("jq expression produced no output")
	}
	if err, isErr := v.(error); isErr {
		return nil, fmt.Errorf("jq execution: %w", err)
	}
	// Drain remaining outputs — we only accept a single result.
	if _, ok2 := iter.Next(); ok2 {
		return nil, fmt.Errorf("jq expression produced multiple outputs; expected exactly one")
	}
	return v, nil
}

// jqVarNameRE is the precompiled regex for valid jq variable identifiers
// (without the leading `$`): [A-Za-z_][A-Za-z0-9_]*.
var jqVarNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// isValidJQVarName reports whether name is a legal jq variable identifier
// (without the leading `$`): [A-Za-z_][A-Za-z0-9_]*.
func isValidJQVarName(name string) bool {
	return jqVarNameRE.MatchString(name)
}
