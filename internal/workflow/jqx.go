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
