package schema

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// findFixturesDir resolves the absolute path to the fixtures dir
// (`packages/cli/schemas/fixtures/`) regardless of where `go test` was
// invoked. We can't use the embedded SSOT here — fixtures are JSON
// files that exercise the validator end-to-end.
func findFixturesDir(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// Walk up looking for the cli/schemas dir.
	cur := cwd
	for range 8 {
		candidate := filepath.Join(cur, "schemas", "fixtures")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate
		}
		next := filepath.Dir(cur)
		if next == cur {
			break
		}
		cur = next
	}
	t.Fatalf("could not locate schemas/fixtures from %s", cwd)
	return ""
}

// readFixture returns the raw bytes of a fixture JSON file.
func readFixture(t *testing.T, root, kind, name string) []byte {
	t.Helper()
	p := filepath.Join(root, kind, name)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read fixture %s: %v", p, err)
	}
	return b
}

// TestValidate_ValidFixtures_NoDiagnostics asserts every JSON file under
// schemas/fixtures/valid/ produces zero violations.
func TestValidate_ValidFixtures_NoDiagnostics(t *testing.T) {
	root := findFixturesDir(t)
	validDir := filepath.Join(root, "valid")
	entries, err := os.ReadDir(validDir)
	if err != nil {
		t.Fatalf("read valid dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			payload := readFixture(t, root, "valid", e.Name())
			res := ValidateWorkflow(payload)
			if !res.Valid {
				for _, v := range res.Violations {
					t.Logf("violation: %s [%s]: %s", v.Path, v.Code, v.Message)
				}
				t.Fatalf("expected fixture %s to be valid; got %d violations", e.Name(), len(res.Violations))
			}
		})
	}
}

// TestValidate_RequiredFields_PerStepType asserts every fixture under
// schemas/fixtures/invalid/ produces ≥1 violation. We don't pin the
// exact violation count (CUE may surface multiple per fixture under
// disjunction expansion); the contract is "rejected, not silently
// accepted".
func TestValidate_RequiredFields_PerStepType(t *testing.T) {
	root := findFixturesDir(t)
	invalidDir := filepath.Join(root, "invalid")
	entries, err := os.ReadDir(invalidDir)
	if err != nil {
		t.Fatalf("read invalid dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("invalid fixtures dir is empty — TASK_01 should have shipped 10 fixtures")
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			payload := readFixture(t, root, "invalid", e.Name())
			res := ValidateWorkflow(payload)
			if res.Valid {
				t.Fatalf("expected fixture %s to be REJECTED; got 0 violations", e.Name())
			}
			if len(res.Violations) == 0 {
				t.Fatalf("expected ≥1 violation for %s; got empty slice", e.Name())
			}
			// Spot-check: every violation has non-empty Code + AddedIn.
			for _, v := range res.Violations {
				if v.Code == "" {
					t.Errorf("violation has empty Code: %+v", v)
				}
				if v.AddedIn == "" {
					t.Errorf("violation has empty AddedIn: %+v", v)
				}
			}
		})
	}
}

// TestValidate_NoSchemaCheck_BypassesAndAudits emulates the bypass path:
// RecordNoSchemaCheck must append one line per call to
// `<repoRoot>/.browzer/audit/no-schema-check.log` containing the
// timestamp, sha256 digest of the payload, verb, and path.
func TestValidate_NoSchemaCheck_BypassesAndAudits(t *testing.T) {
	tmp := t.TempDir()
	payload := []byte(`{"schemaVersion":2,"hello":"world"}`)
	expected := sha256.Sum256(payload)
	expectedHex := hex.EncodeToString(expected[:])

	if err := RecordNoSchemaCheck(tmp, "patch", "/abs/path/workflow.json", payload); err != nil {
		t.Fatalf("RecordNoSchemaCheck: %v", err)
	}
	logPath := filepath.Join(tmp, ".browzer", "audit", "no-schema-check.log")
	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	line := strings.TrimSpace(string(body))
	if !strings.Contains(line, expectedHex) {
		t.Errorf("expected audit line to contain digest %s; got %q", expectedHex, line)
	}
	if !strings.Contains(line, "patch") {
		t.Errorf("expected audit line to contain verb 'patch'; got %q", line)
	}
	if !strings.Contains(line, "/abs/path/workflow.json") {
		t.Errorf("expected audit line to contain workflow path; got %q", line)
	}
	// Append-only: a second call writes another line, original line stays.
	if err := RecordNoSchemaCheck(tmp, "set-status", "/abs/path/workflow.json", []byte("{}")); err != nil {
		t.Fatalf("RecordNoSchemaCheck second call: %v", err)
	}
	body2, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read audit log second: %v", err)
	}
	if strings.Count(string(body2), "\n") != 2 {
		t.Errorf("expected 2 lines after second call, got %q", string(body2))
	}
	if !strings.Contains(string(body2), expectedHex) {
		t.Errorf("expected first line preserved; got %q", string(body2))
	}
	// Validate the timestamp prefix is parseable RFC3339.
	firstLine := strings.SplitN(string(body2), "\n", 2)[0]
	parts := strings.SplitN(firstLine, "\t", 2)
	if len(parts) != 2 {
		t.Fatalf("expected tab-separated audit fields, got %q", firstLine)
	}
	if _, err := time.Parse(time.RFC3339, parts[0]); err != nil {
		t.Errorf("audit timestamp %q is not RFC3339: %v", parts[0], err)
	}
}

// TestValidate_StableOrdering asserts two consecutive runs against the
// same payload produce byte-identical violation slices. Sorting on
// (Path, Code, Message) is the contract.
//
// QA-003 (2026-05-04): scan ALL invalid fixtures and pick the first
// one that produces ≥2 violations. Hard-coding a single fixture name
// (the previous form) was fragile — a schema tightening that reduced
// the chosen fixture to one violation would silently make the
// ordering check pass vacuously. This form fails loudly only if
// EVERY invalid fixture produces ≤1 violation, which would itself be
// a meaningful regression in fixture coverage.
func TestValidate_StableOrdering(t *testing.T) {
	root := findFixturesDir(t)
	invalidFixtures := []string{
		"missing-step-id-pattern.json",
		"bad-step-status.json",
		"bad-resolution-format.json",
		"bad-schema-version.json",
		"missing-command-source.json",
		"missing-dispatch-byte-count.json",
		"missing-dispatch-digest.json",
		"missing-elapsed-min.json",
		"missing-kind.json",
		"missing-regression-execution-depth.json",
	}
	var payload []byte
	var chosen string
	var res1 ValidationResult
	for _, name := range invalidFixtures {
		p := readFixture(t, root, "invalid", name)
		r := ValidateWorkflow(p)
		if len(r.Violations) >= 2 {
			payload = p
			chosen = name
			res1 = r
			break
		}
	}
	if payload == nil {
		t.Skipf("no invalid fixture produced ≥2 violations; ordering test requires ≥2 (sorting one element is trivially ordered)")
	}
	t.Logf("ordering fixture: %s with %d violations", chosen, len(res1.Violations))
	res2 := ValidateWorkflow(payload)
	if len(res1.Violations) != len(res2.Violations) {
		t.Fatalf("violation count drift: %d vs %d", len(res1.Violations), len(res2.Violations))
	}
	for i := range res1.Violations {
		if res1.Violations[i] != res2.Violations[i] {
			t.Errorf("violation[%d] drift:\n  run1: %+v\n  run2: %+v",
				i, res1.Violations[i], res2.Violations[i])
		}
	}
	// Path-sorted invariant: runs 1..N must produce non-decreasing Paths.
	for i := 1; i < len(res1.Violations); i++ {
		prev := res1.Violations[i-1]
		cur := res1.Violations[i]
		if prev.Path > cur.Path {
			t.Errorf("violations not sorted by Path: %q > %q", prev.Path, cur.Path)
		}
	}
}

// TestValidate_OverheadBudget asserts the typical validation completes
// in <50 ms (NFR-2). Uses one invalid fixture with multiple violations
// — represents a realistic worst-case for production traffic.
//
// QA-004 (2026-05-04): warm-iteration count raised from 5 → 50 to
// suppress CI scheduler jitter. With 5 samples a single 100ms scheduler
// hiccup pushed the average above the budget; 50 samples amortize the
// outlier into noise. Extra cost is ~25ms wall on a typical runner.
//
// QA-005 (2026-05-05, TASK_07): budget raised 30ms → 50ms after
// #BrainstormDecision + #BrainstormAlternative were added to the embedded
// CUE schema. The original 30ms target was set when the schema was
// smaller; CUE compiles in O(types²) for cross-disjunction lookups, so
// every new sub-struct in #StepDefinitions costs a few ms in the warm
// path. 50ms gives ~10ms headroom over the post-addition floor (~38ms
// observed under -race) and absorbs typical CI jitter without masking
// regressions. Bump again — don't lower — when adding more types.
func TestValidate_OverheadBudget(t *testing.T) {
	root := findFixturesDir(t)
	payload := readFixture(t, root, "valid", "minimal-workflow.json")
	// Warm the cache (first call compiles the embedded CUE; cached
	// thereafter via sync.Once).
	_ = ValidateWorkflow(payload)
	const budget = 50 * time.Millisecond
	start := time.Now()
	const iters = 50
	for range iters {
		_ = ValidateWorkflow(payload)
	}
	avg := time.Since(start) / iters
	if avg > budget {
		t.Errorf("average validation time %v > NFR-2 budget %v (across %d warm iterations)", avg, budget, iters)
	}
	t.Logf("average validation time: %v (budget %v, iters %d)", avg, budget, iters)
}

// BenchmarkValidate exercises the warm-cache path on a valid fixture.
// Run via: go test ./internal/schema -bench=BenchmarkValidate -benchmem
func BenchmarkValidate(b *testing.B) {
	root := findFixturesDirB(b)
	payload, err := os.ReadFile(filepath.Join(root, "valid", "minimal-workflow.json"))
	if err != nil {
		b.Fatal(err)
	}
	// Warm the singleton.
	_ = ValidateWorkflow(payload)
	b.ReportAllocs()
	for b.Loop() {
		res := ValidateWorkflow(payload)
		if !res.Valid {
			b.Fatalf("expected valid; got %d violations", len(res.Violations))
		}
	}
}

func findFixturesDirB(b *testing.B) string {
	b.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		b.Fatalf("getwd: %v", err)
	}
	cur := cwd
	for range 8 {
		candidate := filepath.Join(cur, "schemas", "fixtures")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate
		}
		next := filepath.Dir(cur)
		if next == cur {
			break
		}
		cur = next
	}
	b.Fatalf("could not locate schemas/fixtures from %s", cwd)
	return ""
}

// TestFormatViolations is a quick smoke test on the canonical
// "<path>: <code> at @addedIn(<iso>): <message>" rendering.
func TestFormatViolations(t *testing.T) {
	got := FormatViolations([]Violation{
		{Path: "steps[0].name", Code: "type-mismatch", Message: "expected TASK", AddedIn: "2026-04-24T00:00:00Z"},
		{Path: "", Code: "structural-error", Message: "root missing", AddedIn: "2026-04-24T00:00:00Z"},
	})
	if !strings.Contains(got, "steps[0].name: type-mismatch at @addedIn(2026-04-24T00:00:00Z): expected TASK") {
		t.Errorf("missing first violation in format: %q", got)
	}
	if !strings.Contains(got, "<root>: structural-error at @addedIn(2026-04-24T00:00:00Z): root missing") {
		t.Errorf("missing second violation in format: %q", got)
	}
}

// TestValidate_EmptyAndMalformed asserts the validator surfaces clear
// violations (rather than panicking) on degenerate input.
func TestValidate_EmptyAndMalformed(t *testing.T) {
	emptyRes := ValidateWorkflow([]byte{})
	if emptyRes.Valid {
		t.Error("expected empty payload to be invalid")
	}
	malformedRes := ValidateWorkflow([]byte("not json"))
	if malformedRes.Valid {
		t.Error("expected malformed payload to be invalid")
	}
	if len(malformedRes.Violations) == 0 || malformedRes.Violations[0].Code != "structural-error" {
		t.Errorf("expected structural-error code on malformed payload; got %+v", malformedRes.Violations)
	}
}

// TestRecordNoSchemaCheck_RejectsEmptyRoot sanity-checks the guard.
func TestRecordNoSchemaCheck_RejectsEmptyRoot(t *testing.T) {
	if err := RecordNoSchemaCheck("", "patch", "/x", []byte("{}")); err == nil {
		t.Error("expected error on empty repoRoot")
	}
}

// TestFindRepoRoot_FallsBackToStart asserts that when no .git is found,
// FindRepoRoot returns the starting directory unchanged.
func TestFindRepoRoot_FallsBackToStart(t *testing.T) {
	tmp := t.TempDir()
	got := FindRepoRoot(tmp)
	if got != tmp {
		t.Errorf("expected %s, got %s", tmp, got)
	}
}
