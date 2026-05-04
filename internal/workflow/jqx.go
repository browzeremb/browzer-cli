package workflow

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/itchyny/gojq"
)

// GetField extracts a value from a parsed JSON document (any) using a
// dot-separated path expression (e.g. "config.mode", "steps").
//
// Return conventions:
//   - Scalar string/number/bool/null values are returned as their plain %v
//     representation (unquoted) when asJSON is false.
//   - When asJSON is true, all values (including scalars) are returned as their
//     JSON encoding.
//   - Objects and arrays are always returned as compact JSON regardless of
//     asJSON.
//   - A missing or null-terminated intermediate key returns an error.
func GetField(data any, path string, asJSON bool) (string, error) {
	parts := strings.Split(path, ".")
	cur := data

	for _, part := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return "", fmt.Errorf("field %q not found: expected object at %q, got %T", path, part, cur)
		}
		val, exists := m[part]
		if !exists {
			return "", fmt.Errorf("field %q not found in document", path)
		}
		cur = val
	}

	// cur is the resolved value.
	switch v := cur.(type) {
	case map[string]any, []any:
		// Always JSON-encode objects and arrays.
		b, err := json.Marshal(v)
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
			b, err := json.Marshal(v)
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
