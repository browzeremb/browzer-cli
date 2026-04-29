package workflow

import (
	"encoding/json"
	"fmt"
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
	q, err := gojq.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("jq parse: %w", err)
	}
	// Disable the gojq `env` / `$ENV` builtin to prevent exfiltrating process
	// environment variables (e.g. BROWZER_TOKEN, AWS_ACCESS_KEY_ID) into
	// workflow.json, which is version-controlled. See OWASP F-sec-4.
	code, err := gojq.Compile(q, gojq.WithEnvironLoader(func() []string { return nil }))
	if err != nil {
		return nil, fmt.Errorf("jq compile: %w", err)
	}
	iter := code.Run(data)
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
