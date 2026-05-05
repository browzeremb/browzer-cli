package commands

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSearchSchemaJSON_DeclaresPath pins the search response schema to
// include the `path` alias of `documentName` introduced for cross-surface
// parity with explore / deps. Regression for SESSION_REPORT_20260505 §3.6.
func TestSearchSchemaJSON_DeclaresPath(t *testing.T) {
	if !strings.Contains(searchSchemaJSON, `"path"`) {
		t.Fatalf("searchSchemaJSON does not declare 'path' field:\n%s", searchSchemaJSON)
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(searchSchemaJSON), &doc); err != nil {
		t.Fatalf("searchSchemaJSON is not valid JSON: %v", err)
	}

	items, _ := doc["items"].(map[string]any)
	if items == nil {
		t.Fatalf("schema is missing 'items'")
	}

	props, _ := items["properties"].(map[string]any)
	if props == nil {
		t.Fatalf("schema items missing 'properties'")
	}
	if _, ok := props["path"]; !ok {
		t.Errorf("schema 'items.properties' missing 'path'; got keys: %v", keysOf(props))
	}
	if _, ok := props["documentName"]; !ok {
		t.Errorf("schema must keep 'documentName' (backwards compat); got keys: %v", keysOf(props))
	}

	required, _ := items["required"].([]any)
	if !containsString(required, "path") {
		t.Errorf("schema 'items.required' must include 'path'; got %v", required)
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func containsString(arr []any, s string) bool {
	for _, v := range arr {
		if got, ok := v.(string); ok && got == s {
			return true
		}
	}
	return false
}
