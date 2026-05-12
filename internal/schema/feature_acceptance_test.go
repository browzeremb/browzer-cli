package schema

// TASK_01 — FEATURE_ACCEPTANCE phase schema completeness tests.
// Verifies mode/modeNote are OPTIONAL (no_mode fixture validates) and the
// extended mode enum (autonomous|hybrid|manual|autonomous-with-stack-boot).

import (
	"path/filepath"
	"testing"
)

func TestFeatureAcceptance_OldHybridMode_Validates(t *testing.T) {
	td := featureAcceptanceTestdata(t)
	res := ValidateWorkflow(mustReadFile(t, filepath.Join(td, "old.json")))
	if !res.Valid {
		for _, v := range res.Violations {
			t.Logf("violation: %s [%s]: %s", v.Path, v.Code, v.Message)
		}
		t.Fatalf("feature_acceptance/old.json must validate (backward compat)")
	}
}

func TestFeatureAcceptance_NewAutonomousWithStackBoot_Validates(t *testing.T) {
	td := featureAcceptanceTestdata(t)
	res := ValidateWorkflow(mustReadFile(t, filepath.Join(td, "new.json")))
	if !res.Valid {
		for _, v := range res.Violations {
			t.Logf("violation: %s [%s]: %s", v.Path, v.Code, v.Message)
		}
		t.Fatalf("feature_acceptance/new.json (mode=autonomous-with-stack-boot) must validate")
	}
}

func TestFeatureAcceptance_NoMode_Validates(t *testing.T) {
	td := featureAcceptanceTestdata(t)
	res := ValidateWorkflow(mustReadFile(t, filepath.Join(td, "no_mode.json")))
	if !res.Valid {
		for _, v := range res.Violations {
			t.Logf("violation: %s [%s]: %s", v.Path, v.Code, v.Message)
		}
		t.Fatalf("feature_acceptance/no_mode.json must validate (mode optional)")
	}
}

func TestFeatureAcceptance_InvalidMode_Rejected(t *testing.T) {
	td := featureAcceptanceTestdata(t)
	res := ValidateWorkflow(mustReadFile(t, filepath.Join(td, "invalid.json")))
	if res.Valid {
		t.Fatalf("feature_acceptance/invalid.json (mode=bogus) must be rejected")
	}
}

func featureAcceptanceTestdata(t *testing.T) string {
	t.Helper()
	cwd := mustGetwd(t)
	cur := cwd
	for range 8 {
		c := filepath.Join(cur, "internal", "schema", "testdata", "feature_acceptance")
		if isDir(c) {
			return c
		}
		c2 := filepath.Join(cur, "testdata", "feature_acceptance")
		if isDir(c2) {
			return c2
		}
		next := filepath.Dir(cur)
		if next == cur {
			break
		}
		cur = next
	}
	t.Fatalf("could not locate testdata/feature_acceptance from %s", cwd)
	return ""
}
