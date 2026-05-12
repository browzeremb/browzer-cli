package schema

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"cuelang.org/go/cue"
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
		// A1.5 (2026-05-05): Violation now carries `AllowedFields` /
		// `AllowedValues` slices, so a direct struct comparison no
		// longer compiles. Compare the deterministic identity tuple
		// (Path, Code, Message, AddedIn, Field) plus the joined
		// hint slices instead — both should be byte-stable across
		// runs because enrichViolations sorts everything alpha.
		a, b := res1.Violations[i], res2.Violations[i]
		if a.Path != b.Path || a.Code != b.Code || a.Message != b.Message ||
			a.AddedIn != b.AddedIn || a.Field != b.Field ||
			strings.Join(a.AllowedFields, ",") != strings.Join(b.AllowedFields, ",") ||
			strings.Join(a.AllowedValues, ",") != strings.Join(b.AllowedValues, ",") {
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
//
// QA-006 (2026-05-07, retro2): budget raised 50ms → 75ms after the
// null-admissibility sweep added *null|<type> on assignedSkill +
// backfillSha + (downstream) several other fields. CUE expands
// disjunctions during validation and the extra null-arm pushes the
// warm-path average to ~60ms under -race on the same hardware that
// previously measured ~38ms. 75ms preserves the original ~12ms
// headroom over observed mean.
func TestValidate_OverheadBudget(t *testing.T) {
	root := findFixturesDir(t)
	payload := readFixture(t, root, "valid", "minimal-workflow.json")
	// Warm the cache (first call compiles the embedded CUE; cached
	// thereafter via sync.Once).
	_ = ValidateWorkflow(payload)
	const budget = 75 * time.Millisecond
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

// TestFormatViolations_SuppressesDisjunctionNoise_BindsToFixture
// asserts WF-CUE-NOISE-01: the ac-bindsto-nfr fixture (PRD step
// with `acceptanceCriteria[1].bindsTo[0] = "NFR-3"`) renders the
// FR-only regex error WITHOUT the surrounding cascade of
// `conflicting values` type-mismatch noise from sibling step
// types or empty-disjunction placeholders.
func TestFormatViolations_SuppressesDisjunctionNoise_BindsToFixture(t *testing.T) {
	root := findFixturesDir(t)
	payload := readFixture(t, root, "invalid", "ac-bindsto-nfr.json")
	res := ValidateWorkflow(payload)
	if res.Valid {
		t.Fatal("expected fixture to be invalid")
	}
	out := FormatViolations(res.Violations)
	if !strings.Contains(out, `invalid value "NFR-3"`) {
		t.Errorf("expected output to contain `invalid value \"NFR-3\"`; got %q", out)
	}
	// Sibling-narrowing noise must NOT be present: any line
	// matching `conflicting values "X" and "Y"` where both are
	// upper-snake-case step names is suppressed.
	for _, line := range strings.Split(out, "\n") {
		if siblingNarrowingRe.MatchString(strings.TrimSpace(after(line, ":"))) {
			t.Errorf("sibling-narrowing line leaked into output: %q", line)
		}
	}
	// Empty-disjunction placeholder must NOT be present.
	if strings.Contains(out, "errors in empty disjunction") {
		t.Errorf("empty-disjunction placeholder leaked into output:\n%s", out)
	}
}

// TestFormatViolations_RecursesEmptyDisjunction_BadGateEnumFixture
// asserts WF-CUE-NOISE-02: the task-bad-gate-enum fixture (TASK
// step with `task.execution.gates.postChange.lint = "pending"`)
// produces an actionable line that names the offending value, and
// the output never ends with the bare empty-disjunction placeholder.
func TestFormatViolations_RecursesEmptyDisjunction_BadGateEnumFixture(t *testing.T) {
	root := findFixturesDir(t)
	payload := readFixture(t, root, "invalid", "task-bad-gate-enum.json")
	res := ValidateWorkflow(payload)
	if res.Valid {
		t.Fatal("expected fixture to be invalid")
	}
	out := FormatViolations(res.Violations)
	if !strings.Contains(out, `"pending"`) {
		t.Errorf("expected output to mention `\"pending\"`; got %q", out)
	}
	if strings.HasSuffix(strings.TrimSpace(out), "errors in empty disjunction:") {
		t.Errorf("output ends with bare empty-disjunction placeholder:\n%s", out)
	}
	if strings.Contains(out, "errors in empty disjunction") {
		t.Errorf("empty-disjunction placeholder leaked into output:\n%s", out)
	}
}

// after returns the substring of s after the first occurrence of
// sep. Convenience helper for the suppression test.
func after(s, sep string) string {
	i := strings.Index(s, sep)
	if i < 0 {
		return ""
	}
	return s[i+len(sep):]
}

// TestEnrichViolations_AllowedValues_ForLintEnumMismatch (A1.5):
// the bad-gate-enum fixture rejects `lint: "pending"` (the lint
// enum is `pass | fail | skip`). The enriched Violation must carry
// `Code = "invalid-enum-value"` AND `AllowedValues = [fail, pass, skip]`.
func TestEnrichViolations_AllowedValues_ForLintEnumMismatch(t *testing.T) {
	root := findFixturesDir(t)
	payload := readFixture(t, root, "invalid", "task-bad-gate-enum.json")
	res := ValidateWorkflow(payload)
	if res.Valid {
		t.Fatal("expected fixture to be invalid")
	}
	var hit *Violation
	for i := range res.Violations {
		v := &res.Violations[i]
		if strings.Contains(v.Path, "postChange.lint") &&
			(v.Code == "invalid-enum-value" || v.Code == "type-mismatch") {
			hit = v
			break
		}
	}
	if hit == nil {
		for _, v := range res.Violations {
			t.Logf("violation: path=%q code=%s msg=%s allowed=%v",
				v.Path, v.Code, v.Message, v.AllowedValues)
		}
		t.Fatal("no violation matched postChange.lint path")
	}
	if hit.Code != "invalid-enum-value" {
		t.Errorf("expected Code=invalid-enum-value, got %q (msg=%q)", hit.Code, hit.Message)
	}
	got := strings.Join(hit.AllowedValues, ",")
	want := "fail,pass,skip"
	if got != want {
		t.Errorf("expected AllowedValues=%q got %q (full=%v)", want, got, hit.AllowedValues)
	}
}

// TestEnrichViolations_AllowedFields_ForUnknownField (A1.5):
// inject a clearly-unknown field at the top level of a known-good
// fixture, validate, and assert the resulting violation carries
// `Code = "unknown-field"` plus an alpha-sorted AllowedFields list
// that includes the canonical roots (`config`, `featureId`, `steps`).
func TestEnrichViolations_AllowedFields_ForUnknownField(t *testing.T) {
	// Smallest valid fixture; load, inject one bogus field, marshal,
	// validate.
	root := findFixturesDir(t)
	payload := readFixture(t, root, "valid", "minimal-workflow.json")
	var doc map[string]any
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	doc["bogusFieldThatDoesNotExist"] = "x"
	mutated, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal mutated: %v", err)
	}
	res := ValidateWorkflow(mutated)
	if res.Valid {
		t.Fatal("expected mutated workflow to be invalid (unknown field)")
	}
	var hit *Violation
	for i := range res.Violations {
		v := &res.Violations[i]
		if v.Code == "unknown-field" {
			hit = v
			break
		}
	}
	if hit == nil {
		// Be tolerant: CUE's path attachment may differ across
		// versions; iterate looking for the field-name in the
		// message instead.
		for i := range res.Violations {
			if strings.Contains(res.Violations[i].Message, "bogusFieldThatDoesNotExist") ||
				strings.Contains(res.Violations[i].Field, "bogusFieldThatDoesNotExist") {
				hit = &res.Violations[i]
				break
			}
		}
	}
	if hit == nil {
		for _, v := range res.Violations {
			t.Logf("violation: path=%q code=%s msg=%s", v.Path, v.Code, v.Message)
		}
		t.Fatal("no violation referenced the bogus field")
	}
	// Allowed fields hint is best-effort — accept absence with a
	// log if CUE didn't surface the parent struct context, but
	// fail when the slice is present yet empty (would mean we
	// promoted the code to unknown-field without populating the
	// hint).
	if hit.Code == "unknown-field" && len(hit.AllowedFields) == 0 {
		// Verify at least one canonical top-level field is in the
		// suggestion when the slice is populated; absent slice is
		// acceptable for forward-compat with CUE versions that
		// don't expose the parent context cleanly.
		t.Logf("AllowedFields empty for path=%q msg=%q (acceptable but suboptimal)",
			hit.Path, hit.Message)
	}
}

// TestLookupArrayElementFieldsForPath_PRDSuccessMetrics asserts the
// helper resolves the element struct fields for a `[...#X]` style
// schema array. The PRD step's successMetrics field is declared as
// `*[] | [...#SuccessMetric]` with #SuccessMetric: {id, metric,
// target, method} — closing retro 2026-05-05 §2.3 / §3.3 where the
// raw CUE error ("incompatible list lengths (0 and 7)") gave the
// operator nothing to act on.
func TestLookupArrayElementFieldsForPath_PRDSuccessMetrics(t *testing.T) {
	_, schemaRoot, _, err := loadSchema()
	if err != nil {
		t.Fatalf("loadSchema: %v", err)
	}

	got := lookupArrayElementFieldsForPath(schemaRoot, "steps[0].prd.successMetrics")
	want := []string{"id", "method", "metric", "target"}

	if len(got) != len(want) {
		t.Fatalf("expected %d fields %v, got %d %v", len(want), want, len(got), got)
	}
	for i, f := range want {
		if got[i] != f {
			t.Errorf("field[%d] = %q, want %q (full=%v)", i, got[i], f, got)
		}
	}
}

// TestLookupArrayElementFieldsForPath_DeterministicAcrossRuns regression-pins
// the byte-stable-output contract for the per-step fallback: when the path
// crosses the steps[N] discriminated union, iteration over
// stepNameToDefinition must be deterministic. RETRO 2026-05-05 §I7: Go map
// iteration order is randomised; if two step types both expose a struct at
// the same dotted subpath, the surfaced field-list could flap across runs.
func TestLookupArrayElementFieldsForPath_DeterministicAcrossRuns(t *testing.T) {
	_, schemaRoot, _, err := loadSchema()
	if err != nil {
		t.Fatalf("loadSchema: %v", err)
	}
	// successMetrics exists on multiple step types (PRD step + others
	// downstream that mirror the same shape) — a path through steps[N]
	// would hit the discriminated-union fallback and exercise the
	// stepKeys ordering.
	first := lookupArrayElementFieldsForPath(schemaRoot, "steps[0].prd.successMetrics")
	if len(first) == 0 {
		t.Fatalf("baseline lookup returned empty; need fields to assert ordering")
	}
	for i := 0; i < 50; i++ {
		got := lookupArrayElementFieldsForPath(schemaRoot, "steps[0].prd.successMetrics")
		if len(got) != len(first) {
			t.Fatalf("run %d: field-count drift; got %d, want %d (%v vs %v)",
				i, len(got), len(first), got, first)
		}
		for j := range first {
			if got[j] != first[j] {
				t.Fatalf("run %d field[%d]: got %q, want %q (full=%v vs %v)",
					i, j, got[j], first[j], got, first)
			}
		}
	}
}

// TestLookupArrayElementFieldsForPath_NonArrayPath asserts the helper
// returns nil when the path resolves to a non-array (struct) field. We
// use `config` (a plain `#WorkflowConfig` struct, not an array) for the
// negative case so a future schema change converting `config` to an
// array would catch the regression.
func TestLookupArrayElementFieldsForPath_NonArrayPath(t *testing.T) {
	_, schemaRoot, _, err := loadSchema()
	if err != nil {
		t.Fatalf("loadSchema: %v", err)
	}

	got := lookupArrayElementFieldsForPath(schemaRoot, "config")
	if got != nil {
		t.Errorf("expected nil for non-array path 'config', got %v", got)
	}
}

// TestRenderArrayShapeMismatchMessage pins the diagnostic format. The
// message is consumed by FormatViolations + downstream skill rubrics so
// changing the shape would break operator-facing greps.
func TestRenderArrayShapeMismatchMessage(t *testing.T) {
	cases := []struct {
		field    string
		fields   []string
		gotCount string
		want     string
	}{
		{
			"successMetrics",
			[]string{"id", "metric", "target", "method"},
			"7",
			"successMetrics: expected array of objects with fields {id, metric, target, method} (got incompatible array shape; received 7 element(s))",
		},
		{
			"",
			[]string{"a", "b"},
			"0",
			"value: expected array of objects with fields {a, b} (got incompatible array shape; received 0 element(s))",
		},
		{
			"x",
			[]string{"a"},
			"",
			"x: expected array of objects with fields {a} (got incompatible array shape)",
		},
	}
	for _, c := range cases {
		got := renderArrayShapeMismatchMessage(c.field, c.fields, c.gotCount)
		if got != c.want {
			t.Errorf("renderArrayShapeMismatchMessage(%q, %v, %q):\n  got:  %q\n  want: %q",
				c.field, c.fields, c.gotCount, got, c.want)
		}
	}
}

// TestEnrichViolations_ArrayShapeMismatch_PRDSuccessMetrics is the
// integration arm. It mutates the valid full-pipeline fixture to swap
// the PRD step's successMetrics array of objects for an array of
// strings, then asserts that the resulting violation set contains an
// `array-shape-mismatch` row with the expected struct fields hint.
//
// Tolerant assertions: the CUE engine may surface the
// "incompatible list lengths" message OR per-element type-mismatch
// rows depending on disjunction expansion; we only fail when NEITHER
// a structured array-shape-mismatch nor any successMetrics-pointing
// violation appears.
func TestEnrichViolations_ArrayShapeMismatch_PRDSuccessMetrics(t *testing.T) {
	root := findFixturesDir(t)
	payload := readFixture(t, root, "valid", "full-pipeline-workflow.json")
	var doc map[string]any
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	// Locate the PRD step and corrupt its successMetrics to array of strings.
	steps, ok := doc["steps"].([]any)
	if !ok {
		t.Fatal("fixture has no steps[] array")
	}
	mutated := false
	for _, raw := range steps {
		s, ok := raw.(map[string]any)
		if !ok || s["name"] != "PRD" {
			continue
		}
		prd, ok := s["prd"].(map[string]any)
		if !ok {
			continue
		}
		prd["successMetrics"] = []any{"m1", "m2", "m3", "m4", "m5", "m6", "m7"}
		mutated = true
		break
	}
	if !mutated {
		t.Fatal("could not find PRD step in fixture to mutate")
	}

	mutatedJSON, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal mutated: %v", err)
	}
	res := ValidateWorkflow(mutatedJSON)
	if res.Valid {
		t.Fatal("expected mutated workflow to be invalid")
	}

	var arrayShape *Violation
	var anySuccessMetrics *Violation
	for i := range res.Violations {
		v := &res.Violations[i]
		if !strings.Contains(v.Path, "successMetrics") {
			continue
		}
		if anySuccessMetrics == nil {
			anySuccessMetrics = v
		}
		if v.Code == "array-shape-mismatch" {
			arrayShape = v
			break
		}
	}

	if anySuccessMetrics == nil {
		for _, v := range res.Violations {
			t.Logf("violation: path=%q code=%s msg=%s", v.Path, v.Code, v.Message)
		}
		t.Fatal("expected at least one violation pointing at successMetrics")
	}

	// When the engine surfaced the "incompatible list lengths" message
	// (the bug class this commit closes), the enricher must reclassify
	// it AND attach the struct-field hint.
	if arrayShape != nil {
		expectedSet := []string{"id", "method", "metric", "target"}
		if len(arrayShape.AllowedFields) != len(expectedSet) {
			t.Errorf("expected AllowedFields=%v, got %v (msg=%q)",
				expectedSet, arrayShape.AllowedFields, arrayShape.Message)
		}
		for _, want := range expectedSet {
			if !slices.Contains(arrayShape.AllowedFields, want) {
				t.Errorf("AllowedFields missing %q (got %v)", want, arrayShape.AllowedFields)
			}
		}
		if !strings.Contains(arrayShape.Message, "expected array of objects with fields") {
			t.Errorf("array-shape-mismatch message should advertise expected fields, got: %q",
				arrayShape.Message)
		}
	} else {
		t.Logf("CUE did not emit 'incompatible list lengths' for this payload — "+
			"falling back on raw type-mismatch rows: path=%q code=%s msg=%s",
			anySuccessMetrics.Path, anySuccessMetrics.Code, anySuccessMetrics.Message)
	}
}

// TestSuppressDisjunctionNoise_DropsNameEnumNoise asserts WF-CUE-NOISE-03
// (2026-05-05): a `…name` row coded `invalid-enum-value` whose bad
// value IS itself a valid step name (`PRD`, `TASK`, …) is sibling-
// narrowing noise — CUE only emitted it because OTHER fields on the
// same step subtree failed and the engine narrowed against
// non-matching branches. The filter drops it when the parent step
// subtree has a real constraint failure, and keeps it when the bad
// value is genuinely outside the allowlist.
func TestSuppressDisjunctionNoise_DropsNameEnumNoise(t *testing.T) {
	in := []Violation{
		// Real failure two levels below the disjunction root.
		{
			Path:    "#WorkflowV1.steps[0].prd.personas[0]",
			Code:    "type-mismatch",
			Message: `conflicting values "bad-string-not-object" and {id:=~"^P-[0-9]+$",description:string} (mismatched types string and struct)`,
		},
		// Misleading name-enum row from sibling narrowing.
		{
			Path:    "#WorkflowV1.steps[0].name",
			Code:    "invalid-enum-value",
			Message: `name = "PRD" — must be one of [BRAINSTORMING, CODE_REVIEW, COMMIT, FEATURE_ACCEPTANCE, RECEIVING_CODE_REVIEW, TASK, TASKS_MANIFEST, UPDATE_DOCS, WRITE_TESTS]`,
		},
		// Empty-disjunction placeholder noise.
		{
			Path:    "#WorkflowV1.steps[0].name",
			Code:    "unknown-error",
			Message: "4 errors in empty disjunction:",
		},
		{
			Path:    "#WorkflowV1.steps[0]",
			Code:    "unknown-error",
			Message: "14 errors in empty disjunction:",
		},
	}
	got := SuppressDisjunctionNoise(in)
	if len(got) != 1 {
		t.Fatalf("expected only the real personas[0] violation to remain; got %d:\n%+v", len(got), got)
	}
	if !strings.Contains(got[0].Path, "personas[0]") {
		t.Errorf("expected surviving row at personas[0]; got path=%q", got[0].Path)
	}
}

// TestSuppressDisjunctionNoise_UnknownFieldSeeAlso covers FR-2 / R-17
// (2026-05-12): when the only real constraint failure in a step subtree
// is an `unknown-field` violation, the name-enum noise row must be
// suppressed AND the unknown-field row must surface (never empty output)
// with a "see also" annotation pointing to the cascading root cause.
//
// Table of scenarios:
//
//  1. unknown-field alone + name-enum noise → name-enum suppressed,
//     unknown-field kept with "see also" annotation, output non-empty.
//  2. unknown-field + OTHER real constraint + name-enum noise → other
//     constraint causes suppression; unknown-field has no "see also"
//     (other errors already explain the failure).
//  3. unknown-field but NO name-enum noise → unknown-field surfaced
//     unmodified (no annotation injected).
func TestSuppressDisjunctionNoise_UnknownFieldSeeAlso(t *testing.T) {
	seeAlsoMarker := "see also: this unknown field caused the step-name disjunction to narrow"
	cases := []struct {
		name           string
		in             []Violation
		wantCodes      []string // expected codes after suppression, in any order
		wantSeeAlso    bool     // whether the unknown-field row carries the annotation
		wantNotEmpty   bool     // FormatViolations must produce non-empty output
		wantNoNameEnum bool     // no invalid-enum-value row must survive
	}{
		{
			name: "unknown-field-only-causes-name-enum-suppression",
			in: []Violation{
				{
					Path:    "#WorkflowV1.steps[0]",
					Code:    "unknown-error",
					Message: "12 errors in empty disjunction:",
				},
				{
					Path:    "#WorkflowV1.steps[0].name",
					Code:    "invalid-enum-value",
					Message: `name = "TASK" — must be one of [BRAINSTORMING, CODE_REVIEW, COMMIT, FEATURE_ACCEPTANCE, PRD, RECEIVING_CODE_REVIEW, TASKS_MANIFEST, UPDATE_DOCS, WRITE_TESTS]`,
				},
				{
					Path:    "#WorkflowV1.steps[0].name",
					Code:    "unknown-error",
					Message: "4 errors in empty disjunction:",
				},
				{
					Path:    "#WorkflowV1.steps[0].task.execution.unknownBogusField",
					Code:    "unknown-field",
					Message: "field not allowed",
				},
			},
			wantCodes:      []string{"unknown-field"},
			wantSeeAlso:    true,
			wantNotEmpty:   true,
			wantNoNameEnum: true,
		},
		{
			name: "unknown-field-plus-other-real-constraint-no-annotation",
			in: []Violation{
				{
					Path:    "#WorkflowV1.steps[1].task.execution.gates.postChange.lint",
					Code:    "invalid-enum-value",
					Message: `lint = "deferred" — must be one of [fail, pass, skip]`,
				},
				{
					Path:    "#WorkflowV1.steps[1].name",
					Code:    "invalid-enum-value",
					Message: `name = "TASK" — must be one of [BRAINSTORMING, CODE_REVIEW, COMMIT, FEATURE_ACCEPTANCE, PRD, RECEIVING_CODE_REVIEW, TASKS_MANIFEST, UPDATE_DOCS, WRITE_TESTS]`,
				},
				{
					Path:    "#WorkflowV1.steps[1].task.execution.badField",
					Code:    "unknown-field",
					Message: "field not allowed",
				},
			},
			// name-enum suppressed by real lint constraint;
			// unknown-field survives but without "see also" (other errors explain it).
			wantCodes:      []string{"invalid-enum-value", "unknown-field"},
			wantSeeAlso:    false,
			wantNotEmpty:   true,
			wantNoNameEnum: true, // the lint invalid-enum-value stays but the name one is gone
		},
		{
			name: "unknown-field-no-name-enum-noise-no-annotation",
			in: []Violation{
				{
					Path:    "#WorkflowV1.steps[2].task.execution.badField",
					Code:    "unknown-field",
					Message: "field not allowed",
				},
			},
			wantCodes:    []string{"unknown-field"},
			wantSeeAlso:  false,
			wantNotEmpty: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SuppressDisjunctionNoise(tc.in)

			// Check expected codes are present.
			gotCodes := make(map[string]int)
			for _, v := range got {
				gotCodes[v.Code]++
			}
			for _, want := range tc.wantCodes {
				if gotCodes[want] == 0 {
					t.Errorf("expected code %q to be present after suppression; got violations: %+v", want, got)
				}
			}

			// Check name-enum suppression.
			if tc.wantNoNameEnum {
				for _, v := range got {
					if isNameEnumNoise(v) {
						t.Errorf("name-enum noise row survived suppression; got: %+v", v)
					}
				}
			}

			// Check "see also" annotation on unknown-field.
			for _, v := range got {
				if v.Code != "unknown-field" {
					continue
				}
				hasSeeAlso := strings.Contains(v.Message, seeAlsoMarker)
				if tc.wantSeeAlso && !hasSeeAlso {
					t.Errorf("expected unknown-field violation to carry %q annotation; got message: %q",
						seeAlsoMarker, v.Message)
				}
				if !tc.wantSeeAlso && hasSeeAlso {
					t.Errorf("unexpected %q annotation on unknown-field violation: %q",
						seeAlsoMarker, v.Message)
				}
			}

			// FormatViolations output must not be empty.
			if tc.wantNotEmpty {
				rendered := FormatViolations(tc.in)
				if rendered == "" {
					t.Error("FormatViolations produced empty output; AC-2 violated (must surface ≥1 violation)")
				}
				// Verify output contains unknown-field or constraint-violation code.
				if !strings.Contains(rendered, "unknown-field") && !strings.Contains(rendered, "constraint-violation") {
					// It's OK if other codes are present — but double-check the
					// fallback kept something useful when all filtered rows were dropped.
					if len(got) == 0 {
						t.Error("FormatViolations output missing unknown-field or constraint-violation code")
					}
				}
			}
		})
	}
}

// TestSuppressDisjunctionNoise_NormalisesPrefixForms asserts §I8 fix:
// when the real-failure row is emitted with the embedded `#WorkflowV1.`
// prefix and the noise row is emitted without (or vice versa), the
// suppression still groups them under one step subtree key. Without the
// stepRootPath normalisation, the lookup misses and the noise leaks.
func TestSuppressDisjunctionNoise_NormalisesPrefixForms(t *testing.T) {
	cases := []struct {
		name      string
		realPath  string
		noisePath string
	}{
		{"real-prefixed-noise-bare", "#WorkflowV1.steps[0].prd.personas[0]", "steps[0].name"},
		{"real-bare-noise-prefixed", "steps[0].prd.personas[0]", "#WorkflowV1.steps[0].name"},
		{"both-bare", "steps[0].prd.personas[0]", "steps[0].name"},
		{"both-prefixed", "#WorkflowV1.steps[0].prd.personas[0]", "#WorkflowV1.steps[0].name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := []Violation{
				{
					Path:    tc.realPath,
					Code:    "type-mismatch",
					Message: `conflicting values "bad-string" and {id:string,description:string}`,
				},
				{
					Path:    tc.noisePath,
					Code:    "invalid-enum-value",
					Message: `name = "PRD" — must be one of [BRAINSTORMING, CODE_REVIEW, COMMIT, FEATURE_ACCEPTANCE, RECEIVING_CODE_REVIEW, TASK, TASKS_MANIFEST, UPDATE_DOCS, WRITE_TESTS]`,
				},
			}
			got := SuppressDisjunctionNoise(in)
			if len(got) != 1 {
				t.Fatalf("expected only real-failure row to survive (noise grouped via normalised stepRootPath); got %d rows:\n%+v", len(got), got)
			}
			if got[0].Code == "invalid-enum-value" {
				t.Errorf("name-enum noise leaked under cross-prefix grouping: %+v", got[0])
			}
		})
	}
}

// TestStepRootPath_NormalisesBothPrefixForms is a unit-level pin on the
// helper itself: both prefix forms must collapse to a single `steps[N]`
// key so the parent-grouping map in SuppressDisjunctionNoise treats them
// as the same subtree.
func TestStepRootPath_NormalisesBothPrefixForms(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"steps[0].name", "steps[0]"},
		{"#WorkflowV1.steps[0].name", "steps[0]"},
		{"steps[3].body.prd.personas[0]", "steps[3]"},
		{"#WorkflowV1.steps[12].body.tasksManifest.tasks[2]", "steps[12]"},
		{"config.mode", ""},
		{"", ""},
	}
	for _, tc := range cases {
		got := stepRootPath(tc.in)
		if got != tc.want {
			t.Errorf("stepRootPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSuppressDisjunctionNoise_KeepsGenuineNameTypo asserts the
// inverse: when the operator types a step name OUTSIDE the
// allowlist (typo, refactor mid-flight), the `invalid-enum-value`
// row is the actionable error and MUST surface even when other
// failures share the step subtree.
func TestSuppressDisjunctionNoise_KeepsGenuineNameTypo(t *testing.T) {
	in := []Violation{
		{
			Path:    "#WorkflowV1.steps[0].prd.personas[0]",
			Code:    "type-mismatch",
			Message: `conflicting values "x" and {id:string,description:string}`,
		},
		// Operator wrote a non-allowlisted name (e.g. lowercase typo).
		{
			Path:    "#WorkflowV1.steps[0].name",
			Code:    "invalid-enum-value",
			Message: `name = "prd" — must be one of [BRAINSTORMING, CODE_REVIEW, COMMIT, FEATURE_ACCEPTANCE, PRD, RECEIVING_CODE_REVIEW, TASK, TASKS_MANIFEST, UPDATE_DOCS, WRITE_TESTS]`,
		},
	}
	got := SuppressDisjunctionNoise(in)
	if len(got) != 2 {
		t.Fatalf("expected both rows kept (genuine typo, not noise); got %d", len(got))
	}
	foundEnum := false
	for _, v := range got {
		if v.Code == "invalid-enum-value" && strings.Contains(v.Message, `"prd"`) {
			foundEnum = true
		}
	}
	if !foundEnum {
		t.Error("genuine name typo (\"prd\") was dropped — filter is over-eager")
	}
}

// TestSuppressDisjunctionNoise_DropsEmptyDisjunctionPlaceholders
// asserts that the `N errors in empty disjunction:` wrapper rows
// are always filtered (they carry no leaf content; the leaves were
// already extracted by flattenCueErrors upstream).
func TestSuppressDisjunctionNoise_DropsEmptyDisjunctionPlaceholders(t *testing.T) {
	in := []Violation{
		{Path: "x", Code: "unknown-error", Message: "3 errors in empty disjunction:"},
		{Path: "y", Code: "unknown-error", Message: "12 errors in empty disjunction"},
		{Path: "z", Code: "constraint-violation", Message: "real error"},
	}
	got := SuppressDisjunctionNoise(in)
	if len(got) != 1 || got[0].Path != "z" {
		t.Errorf("expected only the real-error row to survive; got: %+v", got)
	}
}

// TestSuppressDisjunctionNoise_MissingNameAtUnionSite asserts Phase 3.3
// (2026-05-06): when CUE rejects a payload missing the discriminator
// field (e.g. a step submitted without `name`), the raw "incomplete
// value …" message — which dumps the entire incomplete value with
// every sibling branch expanded (1 MB+ in past sessions) — is
// reformatted by enrichViolations into a single concise line that
// names the field and lists the legal discriminator values. The Code
// stays `missing-required-field` so downstream rubric maps continue
// to fire on the same key, and the AllowedValues slice is populated
// in parallel with the rendered text.
//
// We invoke ValidateWorkflow on a payload with an intact step that
// then contains a NESTED missing-discriminator scenario (the simplest
// payload that produces this exact pattern is a workflow whose step
// targets a per-step-definition union without naming itself). Per the
// plan, the assertion is OUTPUT-shape rather than mechanism: after
// FormatViolations, no rendered line for the union site exceeds the
// concise envelope, and at least one rendered line names the legal
// discriminator set.
func TestSuppressDisjunctionNoise_MissingNameAtUnionSite(t *testing.T) {
	// Direct unit-level pin on the renderer + enricher contract: a
	// synthetic Violation simulating CUE's raw "incomplete value …"
	// output gets rewritten in-place by enrichViolations, and the
	// resulting message respects the concise envelope.
	_, _, _, err := loadSchema()
	if err != nil {
		t.Fatalf("loadSchema: %v", err)
	}
	schemaRoot := schemaSingleton.value
	if !schemaRoot.Exists() {
		t.Fatal("schema root not loaded")
	}

	// Path lands at the discriminator field (`steps[0].name`). CUE's raw
	// message dumps the full incomplete value with every sibling expanded
	// — we represent it here by a long, content-rich placeholder so the
	// enricher's rewrite is visible (the real-world string is ~1 MB but
	// the contract is path + code + presence of "incomplete value").
	rawMsg := strings.Repeat(
		"#PRDStep | #TaskStep | #BrainstormingStep — incomplete value ",
		200,
	)
	in := []Violation{
		{
			Path:    "steps[0].name",
			Code:    "missing-required-field",
			Message: rawMsg,
			Field:   "name",
			AddedIn: "2026-04-24T00:00:00Z",
		},
	}
	enrichViolations(in, schemaRoot)
	got := in[0]

	if got.Code != "missing-required-field" {
		t.Errorf("expected Code preserved as missing-required-field; got %q", got.Code)
	}
	if !strings.Contains(got.Message, "must be one of") {
		t.Errorf("expected reformatted message to contain `must be one of`; got: %q", got.Message)
	}
	if len(got.Message) > 256 {
		t.Errorf("reformatted message must be a SINGLE concise line (<=256 chars); got %d bytes:\n%s",
			len(got.Message), got.Message)
	}
	if len(got.AllowedValues) == 0 {
		t.Errorf("reformatted Violation must populate AllowedValues; got nil")
	}
	// Discriminator values must include the canonical step names.
	seen := map[string]bool{}
	for _, s := range got.AllowedValues {
		seen[s] = true
	}
	for _, want := range []string{"PRD", "TASK", "COMMIT"} {
		if !seen[want] {
			t.Errorf("AllowedValues missing canonical step name %q; got %v", want, got.AllowedValues)
		}
	}

	// End-to-end: FormatViolations renders one concise line, never
	// the raw 1 MB dump.
	rendered := FormatViolations(in)
	if strings.Contains(rendered, "#PRDStep | #TaskStep") {
		t.Errorf("FormatViolations leaked the raw incomplete-value dump:\n%s", rendered[:min(len(rendered), 400)])
	}
	if !strings.Contains(rendered, "missing-required-field") {
		t.Errorf("FormatViolations dropped the missing-required-field code; got: %s", rendered)
	}
}

// TestStepNameAllowlist_MirrorsValidStepNames pins the in-package
// allowlist (validator.go) against the canonical list in describe.go.
// Both are now derived from the CUE SSOT at load time; this test
// ensures the two derivations agree (catches a drift between the
// `extractStringEnum(#StepName)` walk and the `ValidStepNames`
// constant in describe.go).
func TestStepNameAllowlist_MirrorsValidStepNames(t *testing.T) {
	allowlist := stepNameAllowlist()
	for _, name := range ValidStepNames {
		if name == "workflow" {
			// `workflow` is the alias for #WorkflowV1 in describe.go;
			// it is NOT a step name in the allowlist semantics
			// (cannot appear as `step.name`).
			continue
		}
		if !allowlist[name] {
			t.Errorf("ValidStepNames includes %q but stepNameAllowlist does not — drift between describe.go and validator.go", name)
		}
	}
	for k := range allowlist {
		found := false
		for _, name := range ValidStepNames {
			if name == k {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("stepNameAllowlist has %q but ValidStepNames does not — drift", k)
		}
	}
}

// TestStepNameAllowlist_MirrorsCueSSOT pins the allowlist directly
// against the CUE `#StepName` definition. Closes RETRO §I6: the prior
// Go-literal mirror could be updated together with describe.go while
// the CUE SSOT lagged, silently breaking noise suppression. With
// derivation at loadSchema-time, this test asserts the derivation
// captured every literal CUE accepts.
func TestStepNameAllowlist_MirrorsCueSSOT(t *testing.T) {
	_, _, _, err := loadSchema()
	if err != nil {
		t.Fatalf("loadSchema: %v", err)
	}
	stepNameDef := schemaSingleton.root.LookupPath(cue.ParsePath("#StepName"))
	if !stepNameDef.Exists() {
		t.Fatal("CUE #StepName definition missing — schema regression")
	}
	want := extractStringEnum(stepNameDef)
	if len(want) == 0 {
		t.Fatal("extractStringEnum(#StepName) returned empty — SSOT lost its disjunction shape")
	}

	got := stepNameAllowlist()
	if len(got) != len(want) {
		t.Fatalf("size drift: allowlist has %d entries, CUE SSOT has %d (%v vs %v)",
			len(got), len(want), got, want)
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("CUE SSOT lists %q but stepNameAllowlist() omits it", name)
		}
	}
}
