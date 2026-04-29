package workflow

import (
	"encoding/json"
	"strings"
	"testing"
)

// queryFixture is a workflow.json with multiple step types covering every
// surface the registered queries probe: TASK steps with gates + scope
// adjustments, a CODE_REVIEW step with mixed-status findings, a
// FEATURE_ACCEPTANCE step with mixed-resolved operator actions, and a
// FIX_FINDINGS step with dispatches.
const queryFixture = `{
  "schemaVersion": 1,
  "featureId": "feat-q",
  "steps": [
    {
      "stepId": "STEP_01_TASK",
      "name": "TASK",
      "status": "COMPLETED",
      "task": {
        "execution": {
          "files": { "modified": ["a.ts", "b.ts"], "created": ["c.ts"] },
          "gates": { "postChange": { "lint": "pass", "typecheck": "pass", "tests": "fail" } },
          "scopeAdjustments": [
            { "owner": "operator", "reason": "deferred to staging", "adjustment": "smoke deferred", "resolution": null },
            { "owner": "agent",    "reason": "in-scope",            "adjustment": "applied",         "resolution": "ok"  }
          ]
        }
      }
    },
    {
      "stepId": "STEP_02_TASK",
      "name": "TASK",
      "status": "COMPLETED",
      "task": {
        "execution": {
          "files": { "modified": ["b.ts", "d.ts"], "created": [] },
          "gates": { "postChange": { "lint": "pass", "tests": "pass" } },
          "scopeAdjustments": [
            { "owner": "agent", "reason": "deploy-time concern", "adjustment": "skipped — operator-followup", "resolution": null }
          ]
        }
      }
    },
    {
      "stepId": "STEP_03_CODE_REVIEW",
      "name": "CODE_REVIEW",
      "status": "COMPLETED",
      "codeReview": {
        "findings": [
          { "id": "F-1", "severity": "high",   "status": "open",  "description": "high-1" },
          { "id": "F-2", "severity": "medium", "status": "open",  "description": "med-1"  },
          { "id": "F-3", "severity": "low",    "status": "open",  "description": "low-1"  },
          { "id": "F-4", "severity": "high",   "status": "fixed", "description": "high-already-fixed" }
        ]
      }
    },
    {
      "stepId": "STEP_04_FIX_FINDINGS",
      "name": "FIX_FINDINGS",
      "status": "COMPLETED",
      "fixFindings": {
        "dispatches": [
          { "findingId": "F-1", "filesChanged": ["e.ts", "a.ts"] },
          { "findingId": "F-2", "filesChanged": ["f.ts"] }
        ]
      }
    },
    {
      "stepId": "STEP_05_FEATURE_ACCEPTANCE",
      "name": "FEATURE_ACCEPTANCE",
      "status": "PAUSED_PENDING_OPERATOR",
      "featureAcceptance": {
        "operatorActionsRequested": [
          { "ac": "AC-1", "kind": "manual-verification",       "description": "smoke",  "resolved": false },
          { "ac": "AC-2", "kind": "deferred-post-merge",       "description": "soak",   "resolved": false },
          { "ac": "AC-3", "kind": "manual-verification",       "description": "done",   "resolved": true  }
        ]
      }
    }
  ]
}`

// loadFixture returns the queryFixture as map[string]any.
func loadFixture(t *testing.T) map[string]any {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(queryFixture), &raw); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return raw
}

// TestQueryRegistry_NamesStable asserts the registry exposes every documented
// query name. A regression here breaks skill consumers — add a query, add it
// to the expected list.
func TestQueryRegistry_NamesStable(t *testing.T) {
	expected := []string{
		"reused-gates",
		"failed-findings",
		"open-deferred-actions",
		"task-gates-baseline",
		"changed-files",
		"deferred-scope-adjustments",
		"open-findings",
		"next-step-id",
		"cache-warm-deps",
		"cache-warm-mentions",
	}
	registry := QueryRegistry()
	for _, name := range expected {
		if _, ok := registry[name]; !ok {
			t.Errorf("registry missing query %q", name)
		}
	}
	if len(registry) != len(expected) {
		t.Errorf("registry has %d queries, expected %d", len(registry), len(expected))
	}
}

// TestQueryReusedGates asserts gates with non-empty, non-fail verdicts across
// completed TASK steps are returned deduped + sorted; "fail"-verdicts are
// excluded.
func TestQueryReusedGates(t *testing.T) {
	raw := loadFixture(t)
	got, err := queryReusedGates(raw)
	if err != nil {
		t.Fatalf("queryReusedGates: %v", err)
	}
	gates, ok := got.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", got)
	}
	want := []string{"lint", "tests", "typecheck"}
	if len(gates) != len(want) {
		t.Fatalf("expected %v, got %v", want, gates)
	}
	for i, g := range gates {
		if g != want[i] {
			t.Errorf("gates[%d]: expected %q, got %q (full: %v)", i, want[i], g, gates)
		}
	}
}

// TestQueryReusedGates_TestsExcludedWhenAllFail asserts that a gate stays
// excluded when every step's verdict is "fail" (not just one). In the
// original fixture STEP_01 has tests=fail and STEP_02 has tests=pass — the
// union includes tests as reused. After mutating STEP_02 to also fail, no
// step contributes "tests" so it must drop out.
func TestQueryReusedGates_TestsExcludedWhenAllFail(t *testing.T) {
	raw := loadFixture(t)
	steps := raw["steps"].([]any)
	step2 := steps[1].(map[string]any)
	step2["task"].(map[string]any)["execution"].(map[string]any)["gates"].(map[string]any)["postChange"].(map[string]any)["tests"] = "fail"

	got, _ := queryReusedGates(raw)
	gates := got.([]string)
	for _, g := range gates {
		if g == "tests" {
			t.Errorf("expected tests to drop out when all steps mark it fail, got %v", gates)
		}
	}
}

// TestQueryFailedFindings asserts open findings of severity high/medium are
// returned, low+fixed are excluded, and order is high → medium → id.
func TestQueryFailedFindings(t *testing.T) {
	raw := loadFixture(t)
	got, err := queryFailedFindings(raw)
	if err != nil {
		t.Fatalf("queryFailedFindings: %v", err)
	}
	findings, ok := got.([]map[string]any)
	if !ok {
		t.Fatalf("expected []map[string]any, got %T", got)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings (F-1 high open + F-2 medium open), got %d (%v)", len(findings), findings)
	}
	if findings[0]["id"] != "F-1" || findings[0]["severity"] != "high" {
		t.Errorf("expected F-1/high first, got %v", findings[0])
	}
	if findings[1]["id"] != "F-2" || findings[1]["severity"] != "medium" {
		t.Errorf("expected F-2/medium second, got %v", findings[1])
	}
}

// TestQueryOpenDeferredActions asserts unresolved entries are returned and
// resolved ones are excluded; sourceStepId is tagged onto every entry.
func TestQueryOpenDeferredActions(t *testing.T) {
	raw := loadFixture(t)
	got, err := queryOpenDeferredActions(raw)
	if err != nil {
		t.Fatalf("queryOpenDeferredActions: %v", err)
	}
	actions, ok := got.([]map[string]any)
	if !ok {
		t.Fatalf("expected []map[string]any, got %T", got)
	}
	if len(actions) != 2 {
		t.Fatalf("expected 2 unresolved actions, got %d", len(actions))
	}
	for _, a := range actions {
		if resolved, _ := a["resolved"].(bool); resolved {
			t.Errorf("resolved=true entry leaked through: %v", a)
		}
		if a["sourceStepId"] != "STEP_05_FEATURE_ACCEPTANCE" {
			t.Errorf("expected sourceStepId tag, got %v", a["sourceStepId"])
		}
	}
}

// TestQueryTaskGatesBaseline asserts the per-gate map records the latest
// TASK-step verdict for each gate key.
func TestQueryTaskGatesBaseline(t *testing.T) {
	raw := loadFixture(t)
	got, err := queryTaskGatesBaseline(raw)
	if err != nil {
		t.Fatalf("queryTaskGatesBaseline: %v", err)
	}
	baseline, ok := got.(map[string]map[string]any)
	if !ok {
		t.Fatalf("expected map[string]map[string]any, got %T", got)
	}
	// STEP_02 is the latest TASK step; tests went from "fail" → "pass" so
	// the baseline must record "pass".
	tests, ok := baseline["tests"]
	if !ok {
		t.Fatalf("baseline missing tests gate: %v", baseline)
	}
	if tests["verdict"] != "pass" {
		t.Errorf("expected tests verdict=pass (latest), got %v", tests)
	}
	if tests["sourceStepId"] != "STEP_02_TASK" {
		t.Errorf("expected sourceStepId=STEP_02_TASK (latest), got %v", tests)
	}
}

// TestQueryChangedFiles asserts the union of modified+created across TASK
// steps and dispatch.filesChanged across FIX_FINDINGS steps is deduped+sorted.
func TestQueryChangedFiles(t *testing.T) {
	raw := loadFixture(t)
	got, err := queryChangedFiles(raw)
	if err != nil {
		t.Fatalf("queryChangedFiles: %v", err)
	}
	files, ok := got.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", got)
	}
	want := []string{"a.ts", "b.ts", "c.ts", "d.ts", "e.ts", "f.ts"}
	if len(files) != len(want) {
		t.Fatalf("expected %v, got %v", want, files)
	}
	for i, f := range files {
		if f != want[i] {
			t.Errorf("files[%d]: expected %q, got %q", i, want[i], f)
		}
	}
}

// TestQueryDeferredScopeAdjustments asserts adjustments matching the
// deferred-keyword regex are returned with the originating stepId; other
// adjustments are excluded.
func TestQueryDeferredScopeAdjustments(t *testing.T) {
	raw := loadFixture(t)
	got, err := queryDeferredScopeAdjustments(raw)
	if err != nil {
		t.Fatalf("queryDeferredScopeAdjustments: %v", err)
	}
	adjs, ok := got.([]map[string]any)
	if !ok {
		t.Fatalf("expected []map[string]any, got %T", got)
	}
	// STEP_01: owner=operator → matches; STEP_02: reason=deploy-time → matches; STEP_01 row 2 → no match.
	if len(adjs) != 2 {
		t.Fatalf("expected 2 deferred adjustments, got %d (%v)", len(adjs), adjs)
	}
	stepIDs := map[string]bool{}
	for _, a := range adjs {
		if id, _ := a["stepId"].(string); id != "" {
			stepIDs[id] = true
		}
	}
	if !stepIDs["STEP_01_TASK"] || !stepIDs["STEP_02_TASK"] {
		t.Errorf("expected adjustments from both TASK steps, got %v", stepIDs)
	}
}

// TestQueryOpenFindings asserts every status==open finding is returned tagged
// with sourceStepId, regardless of severity.
func TestQueryOpenFindings(t *testing.T) {
	raw := loadFixture(t)
	got, err := queryOpenFindings(raw)
	if err != nil {
		t.Fatalf("queryOpenFindings: %v", err)
	}
	findings, ok := got.([]map[string]any)
	if !ok {
		t.Fatalf("expected []map[string]any, got %T", got)
	}
	if len(findings) != 3 {
		t.Fatalf("expected 3 open findings (F-1/F-2/F-3), got %d", len(findings))
	}
	for _, f := range findings {
		if f["sourceStepId"] != "STEP_03_CODE_REVIEW" {
			t.Errorf("missing sourceStepId tag: %v", f)
		}
	}
}

// TestQueryNextStepID asserts the returned ordinal is max(STEP_NN)+1.
func TestQueryNextStepID(t *testing.T) {
	raw := loadFixture(t)
	got, err := queryNextStepID(raw)
	if err != nil {
		t.Fatalf("queryNextStepID: %v", err)
	}
	n, ok := got.(int)
	if !ok {
		t.Fatalf("expected int, got %T", got)
	}
	if n != 6 {
		t.Errorf("expected 6 (max=5 + 1), got %d", n)
	}
}

// TestQueryNextStepID_EmptyWorkflow asserts the first step ordinal is 1.
func TestQueryNextStepID_EmptyWorkflow(t *testing.T) {
	raw := map[string]any{"schemaVersion": 1, "steps": []any{}}
	got, err := queryNextStepID(raw)
	if err != nil {
		t.Fatalf("queryNextStepID: %v", err)
	}
	if got.(int) != 1 {
		t.Errorf("expected 1, got %v", got)
	}
}

// TestQueryReusedGates_StableSort asserts repeated invocations return gates in
// the same deterministic order — required because skills consume the array
// directly without re-sorting.
func TestQueryReusedGates_StableSort(t *testing.T) {
	raw := loadFixture(t)
	first, _ := queryReusedGates(raw)
	for range 10 {
		got, _ := queryReusedGates(raw)
		a, b := first.([]string), got.([]string)
		if len(a) != len(b) {
			t.Fatalf("length drift: %v vs %v", a, b)
		}
		for j := range a {
			if a[j] != b[j] {
				t.Fatalf("order drift at index %d: %v vs %v", j, a, b)
			}
		}
	}
}

// cacheWarmFixture is a minimal workflow with 2 TASK steps each carrying
// 2 scope files, sharing one file (app/shared.ts) to exercise deduplication.
const cacheWarmFixture = `{
  "schemaVersion": 1,
  "featureId": "feat-cw",
  "featDir": "docs/browzer/feat-cw",
  "steps": [
    {
      "stepId": "STEP_01_TASK_01",
      "name": "TASK",
      "status": "COMPLETED",
      "task": {
        "scope": ["apps/api/src/routes/ask.ts", "apps/api/src/shared.ts"]
      }
    },
    {
      "stepId": "STEP_02_TASK_02",
      "name": "TASK",
      "status": "COMPLETED",
      "task": {
        "scope": ["apps/worker/src/consumers/ingest.ts", "apps/api/src/shared.ts"]
      }
    }
  ]
}`

// loadCacheWarmFixture returns the cacheWarmFixture as map[string]any.
func loadCacheWarmFixture(t *testing.T) map[string]any {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(cacheWarmFixture), &raw); err != nil {
		t.Fatalf("decode cacheWarmFixture: %v", err)
	}
	return raw
}

// TestQueryCacheWarmDeps asserts the query returns 3 unique records (4 scope
// entries minus 1 duplicate) with depsCachePath populated and no
// mentionsCachePath.
func TestQueryCacheWarmDeps(t *testing.T) {
	raw := loadCacheWarmFixture(t)
	got, err := queryCacheWarmDeps(raw)
	if err != nil {
		t.Fatalf("queryCacheWarmDeps: %v", err)
	}
	records, ok := got.([]CacheWarmRecord)
	if !ok {
		t.Fatalf("expected []CacheWarmRecord, got %T", got)
	}
	// 3 unique files: apps/api/src/routes/ask.ts, apps/api/src/shared.ts,
	// apps/worker/src/consumers/ingest.ts (sorted).
	if len(records) != 3 {
		t.Fatalf("expected 3 records (deduped), got %d: %v", len(records), records)
	}
	for _, r := range records {
		if r.File == "" {
			t.Errorf("record has empty file field: %v", r)
		}
		if r.CachePath == "" {
			t.Errorf("record %q has empty depsCachePath", r.File)
		}
		// depsCachePath must embed the featDir and the deps subdir.
		if !containsSubstr(r.CachePath, "docs/browzer/feat-cw/.browzer-cache/deps/") {
			t.Errorf("depsCachePath %q does not contain expected prefix", r.CachePath)
		}
		// mentionsCachePath must NOT be populated for this query.
		if r.MentionsCachePath != "" {
			t.Errorf("depsCachePath query unexpectedly set MentionsCachePath: %v", r)
		}
	}
	// Verify deduplication: no two records share the same file.
	seen := make(map[string]int)
	for i, r := range records {
		seen[r.File]++
		if seen[r.File] > 1 {
			t.Errorf("duplicate file %q at index %d", r.File, i)
		}
	}
}

// TestQueryCacheWarmMentions asserts the query returns 3 unique records with
// mentionsCachePath populated and no depsCachePath.
func TestQueryCacheWarmMentions(t *testing.T) {
	raw := loadCacheWarmFixture(t)
	got, err := queryCacheWarmMentions(raw)
	if err != nil {
		t.Fatalf("queryCacheWarmMentions: %v", err)
	}
	records, ok := got.([]CacheWarmRecord)
	if !ok {
		t.Fatalf("expected []CacheWarmRecord, got %T", got)
	}
	if len(records) != 3 {
		t.Fatalf("expected 3 records (deduped), got %d: %v", len(records), records)
	}
	for _, r := range records {
		if r.File == "" {
			t.Errorf("record has empty file field: %v", r)
		}
		if r.MentionsCachePath == "" {
			t.Errorf("record %q has empty mentionsCachePath", r.File)
		}
		if !containsSubstr(r.MentionsCachePath, "docs/browzer/feat-cw/.browzer-cache/mentions/") {
			t.Errorf("mentionsCachePath %q does not contain expected prefix", r.MentionsCachePath)
		}
		// depsCachePath must NOT be populated for this query.
		if r.CachePath != "" {
			t.Errorf("mentionsCachePath query unexpectedly set depsCachePath: %v", r)
		}
	}
}

// TestQueryCacheWarmDeps_EmptyScope asserts that a workflow with no TASK steps
// returns an empty slice, not nil.
func TestQueryCacheWarmDeps_EmptyScope(t *testing.T) {
	raw := map[string]any{
		"schemaVersion": 1,
		"featDir":       "docs/browzer/feat-empty",
		"steps":         []any{},
	}
	got, err := queryCacheWarmDeps(raw)
	if err != nil {
		t.Fatalf("queryCacheWarmDeps on empty: %v", err)
	}
	records, ok := got.([]CacheWarmRecord)
	if !ok {
		t.Fatalf("expected []CacheWarmRecord, got %T", got)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records for empty scope, got %d", len(records))
	}
}

// TestQueryCacheWarmDeps_SlugEncoding asserts forward slashes in file paths are
// replaced with underscores in the cache path filename component.
func TestQueryCacheWarmDeps_SlugEncoding(t *testing.T) {
	raw := map[string]any{
		"schemaVersion": float64(1),
		"featDir":       "docs/browzer/feat-slug",
		"steps": []any{
			map[string]any{
				"stepId": "STEP_01_TASK_01",
				"name":   StepTask,
				"status": StatusCompleted,
				"task": map[string]any{
					"scope": []any{"apps/api/src/server.ts"},
				},
			},
		},
	}
	got, err := queryCacheWarmDeps(raw)
	if err != nil {
		t.Fatalf("queryCacheWarmDeps: %v", err)
	}
	records := got.([]CacheWarmRecord)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	// Slug must not contain raw slashes (only underscores from the tr replacement).
	slug := records[0].CachePath
	// The cache path ends with <slug>.json — extract the filename part.
	lastSlash := strings.LastIndexByte(slug, '/')
	filename := slug[lastSlash+1:]
	if strings.Contains(filename, "/") {
		t.Errorf("slug filename %q contains a raw slash; expected underscores only", filename)
	}
}

// containsSubstr is a convenience wrapper so test code doesn't import strings.
func containsSubstr(s, sub string) bool {
	return strings.Contains(s, sub)
}
