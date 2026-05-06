package commands

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSearchSchemaJSON_DeclaresPath pins the search response schema to
// include the `path` alias of `documentName` introduced for cross-surface
// parity with explore / deps. Regression for SESSION_REPORT_20260505 §3.6.
//
// Updated for RETRO PR 6 #8 (2026-05-06): the response is now wrapped in
// `{entries: [...]}` (parity with `explore --json`) instead of a bare JSON
// array. The path/documentName invariants now live under
// `properties.entries.items.properties`.
func TestSearchSchemaJSON_DeclaresPath(t *testing.T) {
	if !strings.Contains(searchSchemaJSON, `"path"`) {
		t.Fatalf("searchSchemaJSON does not declare 'path' field:\n%s", searchSchemaJSON)
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(searchSchemaJSON), &doc); err != nil {
		t.Fatalf("searchSchemaJSON is not valid JSON: %v", err)
	}

	if got, _ := doc["type"].(string); got != "object" {
		t.Fatalf("schema top-level type must be 'object' (RETRO PR 6 #8 envelope); got %q", got)
	}
	required, _ := doc["required"].([]any)
	if !containsString(required, "entries") {
		t.Fatalf("schema must require top-level 'entries'; got %v", required)
	}
	properties, _ := doc["properties"].(map[string]any)
	entries, _ := properties["entries"].(map[string]any)
	if entries == nil {
		t.Fatalf("schema is missing 'properties.entries'")
	}
	if got, _ := entries["type"].(string); got != "array" {
		t.Fatalf("'entries' must be an array; got %q", got)
	}
	items, _ := entries["items"].(map[string]any)
	if items == nil {
		t.Fatalf("schema 'entries' is missing 'items'")
	}

	props, _ := items["properties"].(map[string]any)
	if props == nil {
		t.Fatalf("schema entries.items missing 'properties'")
	}
	if _, ok := props["path"]; !ok {
		t.Errorf("schema 'entries.items.properties' missing 'path'; got keys: %v", keysOf(props))
	}
	if _, ok := props["documentName"]; !ok {
		t.Errorf("schema must keep 'documentName' (backwards compat); got keys: %v", keysOf(props))
	}

	itemRequired, _ := items["required"].([]any)
	if !containsString(itemRequired, "path") {
		t.Errorf("schema 'entries.items.required' must include 'path'; got %v", itemRequired)
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
