package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper: build a minimal valid workflow.json scaffold for a feat under
// tmpdir/docs/browzer/<feat>/workflow.json. Returns the absolute path.
func seedWorkflowForFeat(t *testing.T, feat string, content string) (string, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "docs", "browzer", feat)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	wf := filepath.Join(dir, "workflow.json")
	if err := os.WriteFile(wf, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, wf
}

const minimalWorkflow = `{
  "schemaVersion": 2,
  "featureId": "feat-test",
  "featureName": "test",
  "featDir": "docs/browzer/feat-test",
  "operator": {"locale": "en-US"},
  "config": {"mode": "autonomous", "setAt": "2026-05-07T00:00:00Z"},
  "startedAt": "2026-05-07T00:00:00Z",
  "totalSteps": 0,
  "completedSteps": 0,
  "steps": []
}
`

// cueValidWorkflow is a minimal schema-v2 workflow with a CUE-valid featureId
// (must match ^feat-[0-9]{8}-[a-z0-9-]+$). Used by tests that exercise the
// full CUE validation path (BROWZER_NO_SCHEMA_CHECK not set).
const cueValidWorkflow = `{
  "schemaVersion": 2,
  "featureId": "feat-20260507-null-test",
  "featureName": "null admissibility test",
  "featDir": "docs/browzer/feat-20260507-null-test",
  "operator": {"locale": "en-US"},
  "config": {"mode": "autonomous", "setAt": "2026-05-07T00:00:00Z"},
  "startedAt": "2026-05-07T00:00:00Z",
  "updatedAt": "2026-05-07T00:00:00Z",
  "completedAt": null,
  "totalElapsedMin": 0,
  "currentStepId": "",
  "nextStepId": "",
  "totalSteps": 0,
  "completedSteps": 0,
  "notes": [],
  "globalWarnings": [],
  "steps": []
}
`

func TestSaveStep_PRDStdinJSON_UpsertsStep(t *testing.T) {
	root, wf := seedWorkflowForFeat(t, "feat-test", minimalWorkflow)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Setenv("BROWZER_NO_SCHEMA_CHECK", "1") // bypass CUE for test focus
	t.Chdir(root)

	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{"save-step", "PRD", "--id", "feat-test", "--stdin", "--quiet"})
	cmd.SetIn(strings.NewReader(`{"summary":"hello","acceptanceCriteria":[{"id":"AC-1","text":"x"}]}`))
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\nstderr=%s", err, errBuf.String())
	}

	data, err := os.ReadFile(wf)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	steps := doc["steps"].([]any)
	if len(steps) != 1 {
		t.Fatalf("want 1 step, got %d", len(steps))
	}
	step := steps[0].(map[string]any)
	if step["name"] != "PRD" {
		t.Errorf("want PRD step, got %v", step["name"])
	}
	prd := step["prd"].(map[string]any)
	if prd["summary"] != "hello" {
		t.Errorf("want summary=hello, got %v", prd["summary"])
	}
}

func TestSaveStep_MissingID_Errors(t *testing.T) {
	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{"save-step", "PRD", "--stdin"})
	cmd.SetIn(strings.NewReader("{}"))
	var b bytes.Buffer
	cmd.SetOut(&b)
	cmd.SetErr(&b)
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error for missing --id")
	}
}

func TestSaveStep_BadInput_ExitsNonZero(t *testing.T) {
	root, _ := seedWorkflowForFeat(t, "feat-test", minimalWorkflow)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Chdir(root)

	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{"save-step", "PRD", "--id", "feat-test", "--stdin"})
	cmd.SetIn(strings.NewReader("not json"))
	var b bytes.Buffer
	cmd.SetOut(&b)
	cmd.SetErr(&b)
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error for bad payload")
	}
}

func TestSaveStep_MarkdownPRD_ParsesSections(t *testing.T) {
	root, wf := seedWorkflowForFeat(t, "feat-test", minimalWorkflow)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Setenv("BROWZER_NO_SCHEMA_CHECK", "1")
	t.Chdir(root)

	mdPath := filepath.Join(root, "prd.md")
	md := "# PRD — sample\n\n## Overview\n\nHello world\n\n## Acceptance Criteria\n\n- AC-1 (FR-1) first\n- AC-2 second\n"
	if err := os.WriteFile(mdPath, []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{"save-step", "PRD", "--id", "feat-test", "--from", mdPath, "--quiet"})
	var b bytes.Buffer
	cmd.SetOut(&b)
	cmd.SetErr(&b)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\nstderr=%s", err, b.String())
	}

	data, err := os.ReadFile(wf)
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal workflow: %v", err)
	}
	steps, ok := doc["steps"].([]any)
	if !ok || len(steps) == 0 {
		t.Fatalf("steps field missing or empty: %v", doc["steps"])
	}
	prdStep, ok := steps[0].(map[string]any)
	if !ok {
		t.Fatalf("step[0] not a map: %v", steps[0])
	}
	prd, ok := prdStep["prd"].(map[string]any)
	if !ok {
		t.Fatalf("prd field missing or wrong type: %v", prdStep["prd"])
	}
	if prd["overview"] != "Hello world" {
		t.Errorf("overview parsed = %v", prd["overview"])
	}
	if prd["title"] != "sample" {
		t.Errorf("title parsed = %v", prd["title"])
	}
	acs, ok := prd["acceptanceCriteria"].([]any)
	if !ok || len(acs) != 2 {
		t.Fatalf("acceptanceCriteria parsed = %v", prd["acceptanceCriteria"])
	}
	first := acs[0].(map[string]any)
	if first["id"] != "AC-1" || first["text"] != "first" {
		t.Errorf("AC-1 fields wrong: %#v", first)
	}
	binds, _ := first["bindsTo"].([]any)
	if len(binds) != 1 || binds[0] != "FR-1" {
		t.Errorf("AC-1 bindsTo = %#v", first["bindsTo"])
	}
}

func TestParseMarkdownPRD_SectionsAndBullets(t *testing.T) {
	src := []byte(`# PRD — Sample feature

## Overview
This is the elevator pitch. It spans
multiple lines.

## Personas
- P-1: Plugin consumer reading SKILL bodies inside a third-party repo.
- P-2: Maintainer in this monorepo.

## Objectives
- Ship feature
- Stay agnostic

## In Scope
- skills/**

## Out of Scope
- mirror tooling
- CLAUDE.md

## Deliverables
- patched files
- one commit

## Functional Requirements
- FR-1 (must) Title text for FR-1
- FR-2 (should): another requirement
- FR-3 freeform text without parens

## Non-Functional Requirements
- NFR-1 (scope) target=zero diff: documentation-only
- NFR-2 target: byte-identical headers - keeps audit invariant

## Success Metrics
- M-1: monorepo path occurrences → 0 (grep)
- M-2 metric=gate exit code; target=0; method=pnpm browzer:gate

## Acceptance Criteria
- AC-1 (FR-1, FR-2): grep returns zero
- AC-2 (FR-3) follow-up check passes

## Risks
- R-1: parser too lenient - mitigation: round-trip CUE validation
- R-2: regression in jq-helpers - mitigation: byte-identical audit

## Dependencies
- external: cue cli
- internal: packages/cli, packages/skills

## Assumptions
- monorepo on main branch

## Task Granularity
one task per domain bucket
`)
	got := parseMarkdownPRD(src)

	if got["title"] != "Sample feature" {
		t.Fatalf("title = %q, want %q", got["title"], "Sample feature")
	}
	if ov, _ := got["overview"].(string); ov == "" || !strings.Contains(ov, "elevator pitch") {
		t.Fatalf("overview missing/wrong: %q", ov)
	}

	personas, _ := got["personas"].([]map[string]any)
	if len(personas) != 2 || personas[0]["id"] != "P-1" {
		t.Fatalf("personas = %#v", personas)
	}
	if d, _ := personas[0]["description"].(string); !strings.Contains(d, "third-party repo") {
		t.Fatalf("persona[0].description = %q", d)
	}

	if got["objectives"].([]string)[0] != "Ship feature" {
		t.Fatalf("objectives = %#v", got["objectives"])
	}
	if got["inScope"].([]string)[0] != "skills/**" {
		t.Fatalf("inScope = %#v", got["inScope"])
	}
	if got["outOfScope"].([]string)[1] != "CLAUDE.md" {
		t.Fatalf("outOfScope = %#v", got["outOfScope"])
	}
	if got["deliverables"].([]string)[1] != "one commit" {
		t.Fatalf("deliverables = %#v", got["deliverables"])
	}
	if got["assumptions"].([]string)[0] != "monorepo on main branch" {
		t.Fatalf("assumptions = %#v", got["assumptions"])
	}
	if got["taskGranularity"] != "one task per domain bucket" {
		t.Fatalf("taskGranularity = %q", got["taskGranularity"])
	}

	frs, _ := got["functionalRequirements"].([]map[string]any)
	if len(frs) != 3 {
		t.Fatalf("FRs = %#v", frs)
	}
	if frs[0]["id"] != "FR-1" || frs[0]["priority"] != "must" || !strings.Contains(frs[0]["text"].(string), "Title text") {
		t.Fatalf("FR-1 = %#v", frs[0])
	}
	if frs[1]["priority"] != "should" {
		t.Fatalf("FR-2 priority = %v", frs[1]["priority"])
	}
	if frs[2]["text"] != "freeform text without parens" {
		t.Fatalf("FR-3 text = %v", frs[2]["text"])
	}

	nfrs, _ := got["nonFunctionalRequirements"].([]map[string]any)
	if len(nfrs) != 2 {
		t.Fatalf("NFRs len = %d", len(nfrs))
	}
	if nfrs[0]["id"] != "NFR-1" || nfrs[0]["category"] != "scope" || nfrs[0]["target"] != "zero diff" {
		t.Fatalf("NFR-1 = %#v", nfrs[0])
	}
	if nfrs[1]["target"] != "byte-identical headers" {
		t.Fatalf("NFR-2 target = %v", nfrs[1]["target"])
	}

	metrics, _ := got["successMetrics"].([]map[string]any)
	if len(metrics) != 2 {
		t.Fatalf("metrics len = %d", len(metrics))
	}
	if metrics[0]["metric"] != "monorepo path occurrences" || metrics[0]["target"] != "0" || metrics[0]["method"] != "grep" {
		t.Fatalf("M-1 = %#v", metrics[0])
	}
	if metrics[1]["metric"] != "gate exit code" || metrics[1]["target"] != "0" || metrics[1]["method"] != "pnpm browzer:gate" {
		t.Fatalf("M-2 = %#v", metrics[1])
	}

	acs, _ := got["acceptanceCriteria"].([]map[string]any)
	if len(acs) != 2 {
		t.Fatalf("ACs len = %d", len(acs))
	}
	if !equalStrings(acs[0]["bindsTo"].([]string), []string{"FR-1", "FR-2"}) {
		t.Fatalf("AC-1 bindsTo = %#v", acs[0]["bindsTo"])
	}

	risks, _ := got["risks"].([]map[string]any)
	if len(risks) != 2 || risks[0]["mitigation"] != "round-trip CUE validation" {
		t.Fatalf("risks = %#v", risks)
	}

	deps, _ := got["dependencies"].(map[string]any)
	if !equalStrings(deps["external"].([]string), []string{"cue cli"}) {
		t.Fatalf("deps.external = %#v", deps["external"])
	}
	if !equalStrings(deps["internal"].([]string), []string{"packages/cli", "packages/skills"}) {
		t.Fatalf("deps.internal = %#v", deps["internal"])
	}

	if _, ok := got["extraSections"]; ok {
		t.Fatalf("unexpected extraSections: %#v", got["extraSections"])
	}
}

func TestParseMarkdownPRD_UnknownSectionDroppedSilently(t *testing.T) {
	// The CUE #PRD schema rejects extraSections — so unknown H2 sections
	// must be discarded by the parser, never surfaced as a field.
	src := []byte(`# PRD — x
## Functional Requirements
- FR-1 (must) text

## Skills the implementer will need
- update-docs
`)
	got := parseMarkdownPRD(src)
	if _, has := got["extraSections"]; has {
		t.Fatalf("extraSections must not appear in PRD payload: %#v", got["extraSections"])
	}
	frs, _ := got["functionalRequirements"].([]map[string]any)
	if len(frs) != 1 {
		t.Fatalf("expected the known section to still be parsed; got %#v", frs)
	}
}

func TestExtractKV(t *testing.T) {
	cases := []struct {
		s, key, wantVal, wantRem string
	}{
		{"target=zero diff: rest", "target", "zero diff", "rest"},
		{"foo target: x; bar", "target", "x", "foo bar"},
		{"alpha; method=run cmd; beta", "method", "run cmd", "alpha beta"},
		{"no marker here", "target", "", "no marker here"},
	}
	for _, c := range cases {
		v, r := extractKV(c.s, c.key)
		if v != c.wantVal || r != c.wantRem {
			t.Errorf("extractKV(%q, %q) = (%q, %q), want (%q, %q)", c.s, c.key, v, r, c.wantVal, c.wantRem)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestGetStep_PRD_RendersMarkdown(t *testing.T) {
	wfBody := `{
  "schemaVersion": 2,
  "featureId": "feat-test",
  "featureName": "test",
  "featDir": "docs/browzer/feat-test",
  "originalRequest": "make the thing",
  "operator": {"locale": "en-US"},
  "config": {"mode": "autonomous", "setAt": "2026-05-07T00:00:00Z"},
  "startedAt": "2026-05-07T00:00:00Z",
  "steps": [
    {"stepId":"STEP_01_PRD","name":"PRD","status":"COMPLETED","startedAt":"2026-05-07T00:00:00Z","applicability":{"applicable":true},"completedAt":"2026-05-07T00:00:01Z","prd":{"summary":"x","acceptanceCriteria":[{"id":"AC-1","text":"a"}]}}
  ]
}
`
	root, _ := seedWorkflowForFeat(t, "feat-test", wfBody)
	t.Chdir(root)

	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{"get-step", "PRD", "--id", "feat-test"})
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\nstderr=%s", err, errBuf.String())
	}
	got := out.String()
	if !strings.Contains(got, "# PRD") {
		t.Errorf("missing PRD header:\n%s", got)
	}
	if !strings.Contains(got, "make the thing") {
		t.Errorf("missing originalRequest:\n%s", got)
	}
}

func TestGetStep_JSON_EmitsStepView(t *testing.T) {
	wfBody := `{
  "schemaVersion": 2,
  "featureId":"f","featureName":"n","featDir":"d",
  "operator":{"locale":"en-US"},
  "config":{"mode":"autonomous","setAt":"2026-05-07T00:00:00Z"},
  "startedAt":"2026-05-07T00:00:00Z",
  "steps":[{"stepId":"STEP_01_PRD","name":"PRD","status":"COMPLETED","startedAt":"2026-05-07T00:00:00Z","applicability":{"applicable":true},"completedAt":"2026-05-07T00:00:01Z","prd":{"summary":"x"}}]
}
`
	root, _ := seedWorkflowForFeat(t, "feat-test", wfBody)
	t.Chdir(root)

	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{"get-step", "PRD", "--id", "feat-test", "--json"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var view map[string]any
	if err := json.Unmarshal(out.Bytes(), &view); err != nil {
		t.Fatalf("parse json: %v\n%s", err, out.String())
	}
	if view["phase"] != "PRD" || view["stepId"] != "STEP_01_PRD" || view["status"] != "COMPLETED" {
		t.Errorf("StepView fields wrong: %v", view)
	}
}

func TestGetStep_UnknownPhase_ExitsNonZero(t *testing.T) {
	root, _ := seedWorkflowForFeat(t, "feat-test", minimalWorkflow)
	t.Chdir(root)
	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{"get-step", "BOGUS", "--id", "feat-test"})
	var b bytes.Buffer
	cmd.SetOut(&b)
	cmd.SetErr(&b)
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected non-zero exit for unknown phase")
	}
}

// =============================================================
// FR-2 rollup tests (retro2, 2026-05-07)
// =============================================================

// TestSaveStep_RollupCounters_AfterTwoSaves verifies that totalSteps and
// completedSteps are recomputed after every successful save-step call so the
// top-level rollup fields stay in sync with the actual steps[] state (FR-2).
//
// Sequence:
//  1. Save PRD → totalSteps==1, completedSteps==1
//  2. Save BRAINSTORMING → totalSteps==2, completedSteps==2
func TestSaveStep_RollupCounters_AfterTwoSaves(t *testing.T) {
	root, wf := seedWorkflowForFeat(t, "feat-test", minimalWorkflow)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Setenv("BROWZER_NO_SCHEMA_CHECK", "1") // isolate rollup logic from CUE
	t.Chdir(root)

	// Save #1: PRD
	run := func(phase, payload string) {
		t.Helper()
		cmd := NewRootCommand("test")
		cmd.SetArgs([]string{"save-step", phase, "--id", "feat-test", "--stdin", "--quiet"})
		cmd.SetIn(strings.NewReader(payload))
		var errBuf bytes.Buffer
		cmd.SetErr(&errBuf)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("save-step %s: %v\nstderr=%s", phase, err, errBuf.String())
		}
	}

	run("PRD", `{"title":"t","functionalRequirements":[],"acceptanceCriteria":[]}`)

	data, _ := os.ReadFile(wf)
	var doc1 map[string]any
	if err := json.Unmarshal(data, &doc1); err != nil {
		t.Fatalf("unmarshal after PRD save: %v", err)
	}
	if got := doc1["totalSteps"]; got != float64(1) {
		t.Errorf("after PRD save: totalSteps want 1, got %v", got)
	}
	if got := doc1["completedSteps"]; got != float64(1) {
		t.Errorf("after PRD save: completedSteps want 1, got %v", got)
	}

	// Save #2: BRAINSTORMING
	run("BRAINSTORMING", `{"questionsAsked":0,"researchRoundRun":false,"researchAgents":0,"dimensions":{"primaryUser":"dev","jobToBeDone":"test","successSignal":"pass","inScope":[],"outOfScope":[]}}`)

	data2, _ := os.ReadFile(wf)
	var doc2 map[string]any
	if err := json.Unmarshal(data2, &doc2); err != nil {
		t.Fatalf("unmarshal after BRAINSTORMING save: %v", err)
	}
	if got := doc2["totalSteps"]; got != float64(2) {
		t.Errorf("after 2 saves: totalSteps want 2, got %v", got)
	}
	if got := doc2["completedSteps"]; got != float64(2) {
		t.Errorf("after 2 saves: completedSteps want 2, got %v", got)
	}
}

// TestSaveStep_CurrentStepId_PointsToNonCompletedStep verifies that after
// save-step completes a step, currentStepId is updated to the next non-
// completed step (or empty when all complete). FR-2 currentStepId recompute.
func TestSaveStep_CurrentStepId_PointsToNonCompletedStep(t *testing.T) {
	// Seed a workflow with one PRD step already PENDING (no steps yet).
	root, wf := seedWorkflowForFeat(t, "feat-test", minimalWorkflow)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	t.Setenv("BROWZER_NO_SCHEMA_CHECK", "1")
	t.Chdir(root)

	// After saving PRD (→ COMPLETED), all steps are COMPLETED → currentStepId
	// must be empty string.
	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{"save-step", "PRD", "--id", "feat-test", "--stdin", "--quiet"})
	cmd.SetIn(strings.NewReader(`{"title":"t","functionalRequirements":[],"acceptanceCriteria":[]}`))
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("save-step PRD: %v\nstderr=%s", err, errBuf.String())
	}

	data, _ := os.ReadFile(wf)
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// All steps COMPLETED → currentStepId must be "".
	if got := doc["currentStepId"]; got != "" {
		t.Errorf("currentStepId: want empty (all completed), got %q", got)
	}
}

// TestSaveStep_NullAssignedSkill_PassesCUEValidation verifies that a CODE_REVIEW
// payload with assignedSkill: null is accepted end-to-end by save-step including
// CUE schema validation (FR-1 null-admissibility fix).
func TestSaveStep_NullAssignedSkill_PassesCUEValidation(t *testing.T) {
	root, _ := seedWorkflowForFeat(t, "feat-20260507-null-test", cueValidWorkflow)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	// Do NOT set BROWZER_NO_SCHEMA_CHECK — we want CUE to run.
	t.Chdir(root)

	payload := `{
  "dispatchMode": "parallel-with-consolidator",
  "reviewTier": "recommended",
  "mandatoryMembers": ["senior-engineer"],
  "duplicationFindings": [],
  "findings": [
    {
      "id": "F-1",
      "domain": "go",
      "severity": "medium",
      "category": "correctness",
      "file": "packages/cli/internal/workflow/apply.go",
      "description": "null assignedSkill test",
      "suggestedFix": "",
      "assignedSkill": null,
      "status": "open"
    }
  ]
}`
	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{"save-step", "CODE_REVIEW", "--id", "feat-20260507-null-test", "--stdin", "--quiet"})
	cmd.SetIn(strings.NewReader(payload))
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("save-step CODE_REVIEW with assignedSkill:null failed (FR-1 regression): %v\nstderr=%s", err, errBuf.String())
	}
}

// TestSaveStep_NullBackfillSha_PassesCUEValidation verifies that a COMMIT payload
// with backfillSha: null is accepted end-to-end by save-step including CUE
// schema validation (FR-1 null-admissibility fix).
func TestSaveStep_NullBackfillSha_PassesCUEValidation(t *testing.T) {
	root, _ := seedWorkflowForFeat(t, "feat-20260507-null-test", cueValidWorkflow)
	t.Setenv("BROWZER_WORKFLOW_MODE", "sync")
	// Do NOT set BROWZER_NO_SCHEMA_CHECK — we want CUE to run.
	t.Chdir(root)

	payload := `{
  "conventionalType": "feat",
  "scope": "cli",
  "subject": "null backfillSha acceptance test",
  "backfillSha": null,
  "trailers": []
}`
	cmd := NewRootCommand("test")
	cmd.SetArgs([]string{"save-step", "COMMIT", "--id", "feat-20260507-null-test", "--stdin", "--quiet"})
	cmd.SetIn(strings.NewReader(payload))
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("save-step COMMIT with backfillSha:null failed (FR-1 regression): %v\nstderr=%s", err, errBuf.String())
	}
}
