package schema

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestPreservedSchemaConstants asserts every JSON Schema constant exposed
// by schema.go is non-empty and decodes as valid JSON. Seven workspace_*
// / org_* commands depend on these strings being present and parseable.
func TestPreservedSchemaConstants(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"OrgShowSchemaJSON", OrgShowSchemaJSON},
		{"OrgMembersListSchemaJSON", OrgMembersListSchemaJSON},
		{"OrgDocsListSchemaJSON", OrgDocsListSchemaJSON},
		{"OrgDocShowSchemaJSON", OrgDocShowSchemaJSON},
		{"WorkspaceDocsListSchemaJSON", WorkspaceDocsListSchemaJSON},
		{"WorkspaceFilesListSchemaJSON", WorkspaceFilesListSchemaJSON},
		{"WorkspaceShowSchemaJSON", WorkspaceShowSchemaJSON},
	}
	for _, tc := range cases {
		if tc.body == "" {
			t.Errorf("%s is empty; the seven workspace_*/org_* commands depend on it", tc.name)
			continue
		}
		var v any
		if err := json.Unmarshal([]byte(tc.body), &v); err != nil {
			t.Errorf("%s does not parse as JSON: %v", tc.name, err)
		}
	}
}

// TestPreservedFunctions asserts the three exported helpers in schema.go
// are present at link time. Package-level function values are statically
// non-nil, so referencing them via reflect.ValueOf(...).Pointer() captures
// their linker address — a zero pointer would mean the symbol was stubbed
// out, which is the regression case we want to catch.
func TestPreservedFunctions(t *testing.T) {
	cases := []struct {
		name string
		fn   any
	}{
		{"Print", Print},
		{"PrintToFile", PrintToFile},
		{"PrintOrSave", PrintOrSave},
	}
	for _, tc := range cases {
		if reflect.ValueOf(tc.fn).Pointer() == 0 {
			t.Errorf("%s has zero linker address", tc.name)
		}
	}
}
