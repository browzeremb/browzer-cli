package schema

// Shared helpers for TASK_01 phase schema completeness tests
// (code_review_test.go, write_tests_test.go, update_docs_test.go,
// feature_acceptance_test.go). Resolves testdata/ adjacent to this
// package regardless of the cwd `go test` was invoked from.

import (
	"os"
	"testing"
)

func mustGetwd(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return cwd
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func mustReadFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return b
}
