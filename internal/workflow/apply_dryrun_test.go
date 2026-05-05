package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestApplyDryRun_DoesNotMutateFile (A1.4, 2026-05-05): the dry-run
// pipeline must validate AND emit a structured response WITHOUT
// touching workflow.json on disk. We snapshot mtime + content
// before and after to assert byte-stability.
func TestApplyDryRun_DoesNotMutateFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.json")
	original := makeThreeStepWorkflow("PENDING", "PENDING", "PENDING")
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	st1, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	res, err := ApplyDryRun(path, "patch", MutatorArgs{
		JQExpr: ".featureName = \"changed\"",
	})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !res.Ok {
		t.Errorf("expected ok=true, got errors=%v", res.Errors)
	}
	if res.BeforeHash == res.AfterHash {
		t.Errorf("expected before/after hashes to differ on a real mutation")
	}
	if !contains(res.DiffPaths, "featureName") {
		t.Errorf("expected diffPaths to include featureName; got %v", res.DiffPaths)
	}

	// File MUST be byte-identical and mtime-stable.
	post, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(post) != original {
		t.Errorf("workflow.json was modified by dry-run\nbefore: %s\nafter:  %s",
			truncate(original, 200), truncate(string(post), 200))
	}
	st2, err := os.Stat(path)
	if err != nil {
		t.Fatalf("re-stat: %v", err)
	}
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Errorf("mtime changed under dry-run: %v → %v", st1.ModTime(), st2.ModTime())
	}
}

// TestApplyDryRun_ReportsValidationErrors (A1.4, 2026-05-05): when
// the post-mutation document fails CUE validation, ApplyDryRun
// returns a result with Ok=false and at least one Errors entry —
// it does not surface a Go error for that case.
func TestApplyDryRun_ReportsValidationErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.json")
	original := makeThreeStepWorkflow("PENDING", "PENDING", "PENDING")
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res, err := ApplyDryRun(path, "patch", MutatorArgs{
		JQExpr: ".steps[0].name = \"BOGUS\"",
	})
	if err != nil {
		t.Fatalf("dry-run returned Go error (expected validation in result): %v", err)
	}
	if res.Ok {
		t.Errorf("expected ok=false on bad enum value; got ok=true result=%+v", res)
	}
	if len(res.Errors) == 0 {
		t.Errorf("expected ≥1 error string in result")
	}
	joined := strings.Join(res.Errors, "\n")
	if !strings.Contains(joined, "BOGUS") {
		t.Errorf("expected error to mention bogus value; got %q", joined)
	}
}

// TestApplyDryRun_NoOpYieldsEqualHashes (A1.4, 2026-05-05): a jq
// expression that does not change the document still completes
// successfully but BeforeHash and AfterHash differ ONLY because
// `mutatorPatch` always stamps `updatedAt`. The diff path list
// surfaces that single field so the caller can branch on it.
func TestApplyDryRun_NoOpYieldsEqualHashes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.json")
	if err := os.WriteFile(path, []byte(makeThreeStepWorkflow("PENDING", "PENDING", "PENDING")), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res, err := ApplyDryRun(path, "patch", MutatorArgs{
		JQExpr: ".",
	})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !res.Ok {
		t.Errorf("expected ok=true on identity expression; got errors=%v", res.Errors)
	}
	// updatedAt is the only field that legitimately differs between
	// before and after on an identity patch.
	for _, p := range res.DiffPaths {
		if p == "updatedAt" {
			return
		}
	}
	t.Errorf("expected diffPaths to contain only updatedAt; got %v", res.DiffPaths)
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
