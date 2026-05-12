package schema

// TASK_01 — UPDATE_DOCS phase schema completeness tests.
// Verifies the optional signals[] and enoentScan additive fields.

import (
	"path/filepath"
	"testing"
)

func TestUpdateDocs_OldShape_Validates(t *testing.T) {
	td := updateDocsTestdata(t)
	res := ValidateWorkflow(mustReadFile(t, filepath.Join(td, "old.json")))
	if !res.Valid {
		for _, v := range res.Violations {
			t.Logf("violation: %s [%s]: %s", v.Path, v.Code, v.Message)
		}
		t.Fatalf("update_docs/old.json must validate (backward compat)")
	}
}

func TestUpdateDocs_NewShape_Validates(t *testing.T) {
	td := updateDocsTestdata(t)
	res := ValidateWorkflow(mustReadFile(t, filepath.Join(td, "new.json")))
	if !res.Valid {
		for _, v := range res.Violations {
			t.Logf("violation: %s [%s]: %s", v.Path, v.Code, v.Message)
		}
		t.Fatalf("update_docs/new.json (signals[] + enoentScan) must validate")
	}
}

func updateDocsTestdata(t *testing.T) string {
	t.Helper()
	cwd := mustGetwd(t)
	cur := cwd
	for range 8 {
		c := filepath.Join(cur, "internal", "schema", "testdata", "update_docs")
		if isDir(c) {
			return c
		}
		c2 := filepath.Join(cur, "testdata", "update_docs")
		if isDir(c2) {
			return c2
		}
		next := filepath.Dir(cur)
		if next == cur {
			break
		}
		cur = next
	}
	t.Fatalf("could not locate testdata/update_docs from %s", cwd)
	return ""
}
