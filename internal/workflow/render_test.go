package workflow

import (
	"encoding/json"
	"strings"
	"testing"
)

// taskStepFixture builds a fully-populated TASK step for render tests.
func taskStepFixture(t *testing.T) Step {
	t.Helper()
	taskPayload := map[string]any{
		"title": "My Task Title",
		"scope": []any{"pkg/foo.go", "pkg/bar.go"},
		"suggestedModel": "sonnet",
		"trivial": false,
		"invariants": []any{
			map[string]any{"rule": "invariant one", "source": "PRD FR-1"},
			map[string]any{"rule": "invariant two", "source": "PRD FR-2"},
		},
		"reviewer": map[string]any{
			"tddDecision": map[string]any{
				"applicable": true,
				"reason":     "clear I/O",
			},
			"testSpecs": []any{
				map[string]any{"type": "red", "description": "red test 1"},
				map[string]any{"type": "red", "description": "red test 2"},
				map[string]any{"type": "green", "description": "green test 1"},
			},
		},
		"explorer": map[string]any{
			"skillsFound": []any{
				map[string]any{"skill": "go-best-practices", "domain": "go"},
				map[string]any{"skill": "superpowers:tdd", "domain": "testing"},
			},
		},
	}
	raw, err := json.Marshal(taskPayload)
	if err != nil {
		t.Fatalf("taskStepFixture: marshal: %v", err)
	}
	completedAt := "2026-04-29T01:00:00Z"
	return Step{
		StepID:      "STEP_04_TASK_01",
		Name:        StepTask,
		TaskID:      "TASK_01",
		Status:      StatusCompleted,
		CompletedAt: &completedAt,
		Task:        json.RawMessage(raw),
	}
}

// minimalTaskStepFixture builds a minimally-populated TASK step (no optional fields).
func minimalTaskStepFixture(t *testing.T) Step {
	t.Helper()
	taskPayload := map[string]any{
		"title": "Minimal Task",
	}
	raw, err := json.Marshal(taskPayload)
	if err != nil {
		t.Fatalf("minimalTaskStepFixture: marshal: %v", err)
	}
	return Step{
		StepID: "STEP_04_TASK_01",
		Name:   StepTask,
		Task:   json.RawMessage(raw),
	}
}

// TestRender_ExecuteTask_AllFieldsPresent asserts that a fully-populated TASK step
// renders a block containing each key field's value.
func TestRender_ExecuteTask_AllFieldsPresent(t *testing.T) {
	step := taskStepFixture(t)

	out, err := Render(step, "execute-task")
	if err != nil {
		t.Fatalf("Render: unexpected error: %v", err)
	}

	checks := []struct {
		label string
		want  string
	}{
		{"title", "My Task Title"},
		{"scope", "pkg/foo.go"},
		{"scope second", "pkg/bar.go"},
		{"TDD applicable", "yes"},
		{"TDD reason", "clear I/O"},
		{"red count", "2 red"},
		{"green count", "1 green"},
		{"skills", "go-best-practices"},
		{"skills second", "superpowers:tdd"},
		{"invariant rule", "invariant one"},
		{"invariant source", "PRD FR-1"},
		{"invariant rule 2", "invariant two"},
		{"suggested model", "sonnet"},
		{"trivial", "no"},
	}

	for _, c := range checks {
		if !strings.Contains(out, c.want) {
			t.Errorf("[%s] expected output to contain %q\nfull output:\n%s", c.label, c.want, out)
		}
	}
}

// TestRender_ExecuteTask_MissingOptionalFields asserts that a minimally-populated TASK step
// renders without panicking, and outputs sensible "(none)" defaults.
func TestRender_ExecuteTask_MissingOptionalFields(t *testing.T) {
	step := minimalTaskStepFixture(t)

	out, err := Render(step, "execute-task")
	if err != nil {
		t.Fatalf("Render: unexpected error for minimal step: %v", err)
	}

	if !strings.Contains(out, "Minimal Task") {
		t.Errorf("expected output to contain title %q\nfull output:\n%s", "Minimal Task", out)
	}
	if !strings.Contains(out, "(none)") {
		t.Errorf("expected output to contain %q for missing optional fields\nfull output:\n%s", "(none)", out)
	}
}

// TestRender_UnknownTemplate asserts that calling Render with an unknown template
// returns a descriptive error naming the template and all 5 known templates.
func TestRender_UnknownTemplate(t *testing.T) {
	step := taskStepFixture(t)

	_, err := Render(step, "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown template, got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("expected error to name the unknown template %q, got: %v", "nonexistent", err)
	}
	for _, known := range []string{"execute-task", "code-review", "brainstorming", "update-docs", "generate-task"} {
		if !strings.Contains(err.Error(), known) {
			t.Errorf("expected error to mention known template %q, got: %v", known, err)
		}
	}
}

// TestRender_StepIsNotTask asserts that calling Render with "execute-task" on a
// non-TASK step returns an error explaining the mismatch.
func TestRender_StepIsNotTask(t *testing.T) {
	prdPayload := map[string]any{"summary": "prd summary"}
	raw, _ := json.Marshal(prdPayload)
	prdStep := Step{
		StepID: "STEP_02_PRD",
		Name:   StepPRD,
		Task:   json.RawMessage(raw),
	}

	_, err := Render(prdStep, "execute-task")
	if err == nil {
		t.Fatal("expected error for non-TASK step with execute-task template, got nil")
	}
	if !strings.Contains(err.Error(), "TASK") {
		t.Errorf("expected error to mention TASK step requirement, got: %v", err)
	}
}

// ── code-review fixtures ──────────────────────────────────────────────────────

func codeReviewStepFixture(t *testing.T) Step {
	t.Helper()
	payload := map[string]any{
		"mode": "parallel-with-consolidator",
		"tier": "recommended",
		"scope": map[string]any{
			"files": 49,
			"lines": 764,
			"tier":  "large",
		},
		"agentTeam": map[string]any{
			"roundTrips": 9,
			"teammates": []any{
				map[string]any{"name": "senior-engineer", "lane": "lint+invariants"},
				map[string]any{"name": "qa", "lane": "test-coverage"},
			},
		},
		"findings": []any{
			map[string]any{"id": "F-SE-1", "severity": "high", "description": "Missing validation on path input"},
			map[string]any{"id": "F-SE-2", "severity": "high", "description": "Unbounded lock map growth"},
			map[string]any{"id": "F-QA-1", "severity": "medium", "description": "No test for concurrent append"},
			map[string]any{"id": "F-QA-2", "severity": "low", "description": "Stale comment in lock.go"},
		},
		"severityCounts": map[string]any{
			"high":   2,
			"medium": 1,
			"low":    1,
		},
		"themes": []any{
			"Validation invariants across multiple files",
			"Lock semantics need hardening",
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("codeReviewStepFixture: marshal: %v", err)
	}
	completedAt := "2026-04-29T06:00:00Z"
	return Step{
		StepID:      "STEP_07_CODE_REVIEW",
		Name:        StepCodeReview,
		Status:      StatusCompleted,
		CompletedAt: &completedAt,
		Task:        json.RawMessage(raw),
	}
}

func minimalCodeReviewStepFixture(t *testing.T) Step {
	t.Helper()
	payload := map[string]any{
		"tier": "basic",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("minimalCodeReviewStepFixture: marshal: %v", err)
	}
	return Step{
		StepID: "STEP_07_CODE_REVIEW",
		Name:   StepCodeReview,
		Task:   json.RawMessage(raw),
	}
}

func TestRender_CodeReview_AllFieldsPresent(t *testing.T) {
	step := codeReviewStepFixture(t)

	out, err := Render(step, "code-review")
	if err != nil {
		t.Fatalf("Render: unexpected error: %v", err)
	}

	checks := []struct {
		label string
		want  string
	}{
		{"mode", "parallel-with-consolidator"},
		{"tier", "recommended"},
		{"scope files", "49"},
		{"scope lines", "764"},
		{"scope tier", "large"},
		{"reviewer name", "senior-engineer"},
		{"reviewer lane", "lint+invariants"},
		{"findings count", "4 total"},
		{"high count", "2H"},
		{"medium count", "1M"},
		{"low count", "1L"},
		{"round trips", "9"},
		{"high finding id", "F-SE-1"},
		{"high finding desc", "Missing validation on path input"},
		{"theme", "Validation invariants"},
		{"findings label", "Findings:"},
		{"reviewers label", "Reviewers:"},
	}

	for _, c := range checks {
		if !strings.Contains(out, c.want) {
			t.Errorf("[%s] expected output to contain %q\nfull output:\n%s", c.label, c.want, out)
		}
	}
}

func TestRender_CodeReview_MissingOptionalFields(t *testing.T) {
	step := minimalCodeReviewStepFixture(t)

	out, err := Render(step, "code-review")
	if err != nil {
		t.Fatalf("Render: unexpected error for minimal step: %v", err)
	}

	if !strings.Contains(out, "Tier: basic") {
		t.Errorf("expected output to contain tier\nfull output:\n%s", out)
	}
	if !strings.Contains(out, "0 total") {
		t.Errorf("expected 0 total findings\nfull output:\n%s", out)
	}
	if !strings.Contains(out, "(none)") {
		t.Errorf("expected (none) for missing optional fields\nfull output:\n%s", out)
	}
}

func TestRender_CodeReview_WrongStepType(t *testing.T) {
	step := taskStepFixture(t) // TASK step, not CODE_REVIEW

	_, err := Render(step, "code-review")
	if err == nil {
		t.Fatal("expected error for wrong step type, got nil")
	}
	if !strings.Contains(err.Error(), "code-review") {
		t.Errorf("expected error to mention template name %q, got: %v", "code-review", err)
	}
	if !strings.Contains(err.Error(), string(StepTask)) {
		t.Errorf("expected error to mention actual step name %q, got: %v", StepTask, err)
	}
}

// ── brainstorming fixtures ────────────────────────────────────────────────────

func brainstormingStepFixture(t *testing.T) Step {
	t.Helper()
	payload := map[string]any{
		"primaryUser":    "platform engineers running workflow skills",
		"jobToBeDone":    "eliminate raw jq boilerplate from workflow skills",
		"successSignal":  "all 9 skills read/write via browzer workflow subcommands",
		"inScope":        []any{"new browzer workflow command group", "flock advisory lock"},
		"outOfScope":     []any{"no schema changes", "no remote workflow.json"},
		"repoSurface":    []any{"packages/cli/internal/workflow/", "packages/skills/skills/"},
		"openQuestions":  []any{"Where should JSON Schema live?", "Measure token baseline?"},
		"assumptions":    []any{"Go 1.22+ available", "flock is sufficient for concurrency"},
		"acceptanceCriteria": []any{
			"browzer workflow get-step returns step JSON",
			"all 9 skills migrated",
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("brainstormingStepFixture: marshal: %v", err)
	}
	completedAt := "2026-04-29T04:25:19Z"
	return Step{
		StepID:      "STEP_01_BRAINSTORMING",
		Name:        StepBrainstorming,
		Status:      StatusCompleted,
		CompletedAt: &completedAt,
		Task:        json.RawMessage(raw),
	}
}

func minimalBrainstormingStepFixture(t *testing.T) Step {
	t.Helper()
	payload := map[string]any{
		"primaryUser": "developer",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("minimalBrainstormingStepFixture: marshal: %v", err)
	}
	return Step{
		StepID: "STEP_01_BRAINSTORMING",
		Name:   StepBrainstorming,
		Task:   json.RawMessage(raw),
	}
}

func TestRender_Brainstorming_AllFieldsPresent(t *testing.T) {
	step := brainstormingStepFixture(t)

	out, err := Render(step, "brainstorming")
	if err != nil {
		t.Fatalf("Render: unexpected error: %v", err)
	}

	checks := []struct {
		label string
		want  string
	}{
		{"primary user", "platform engineers running workflow skills"},
		{"job to be done", "eliminate raw jq boilerplate"},
		{"success signal", "all 9 skills"},
		{"in scope", "new browzer workflow command group"},
		{"out of scope", "no schema changes"},
		{"repo surface", "packages/cli/internal/workflow/"},
		{"open question", "Where should JSON Schema live?"},
		{"assumption", "Go 1.22+ available"},
		{"acceptance criteria count", "2 declared"},
		{"primary user label", "Primary user:"},
		{"job label", "Job-to-be-done:"},
	}

	for _, c := range checks {
		if !strings.Contains(out, c.want) {
			t.Errorf("[%s] expected output to contain %q\nfull output:\n%s", c.label, c.want, out)
		}
	}
}

func TestRender_Brainstorming_MissingOptionalFields(t *testing.T) {
	step := minimalBrainstormingStepFixture(t)

	out, err := Render(step, "brainstorming")
	if err != nil {
		t.Fatalf("Render: unexpected error for minimal step: %v", err)
	}

	if !strings.Contains(out, "developer") {
		t.Errorf("expected output to contain primary user\nfull output:\n%s", out)
	}
	if !strings.Contains(out, "(none)") {
		t.Errorf("expected (none) for missing optional fields\nfull output:\n%s", out)
	}
	if !strings.Contains(out, "0 declared") {
		t.Errorf("expected 0 declared for empty acceptance criteria\nfull output:\n%s", out)
	}
}

func TestRender_Brainstorming_WrongStepType(t *testing.T) {
	step := taskStepFixture(t) // TASK step, not BRAINSTORMING

	_, err := Render(step, "brainstorming")
	if err == nil {
		t.Fatal("expected error for wrong step type, got nil")
	}
	if !strings.Contains(err.Error(), "brainstorming") {
		t.Errorf("expected error to mention template name %q, got: %v", "brainstorming", err)
	}
	if !strings.Contains(err.Error(), string(StepTask)) {
		t.Errorf("expected error to mention actual step name %q, got: %v", StepTask, err)
	}
}

// ── update-docs fixtures ──────────────────────────────────────────────────────

func updateDocsStepFixture(t *testing.T) Step {
	t.Helper()
	payload := map[string]any{
		"mode":       "anchor-only",
		"budgetUsed": 8,
		"budgetMax":  20,
		"budgetTier": "large",
		"docsMentioning": []any{
			map[string]any{"sourceFile": "packages/skills/references/workflow-schema.md"},
			map[string]any{"sourceFile": "packages/cli/CLAUDE.md"},
		},
		"anchorDocsAlwaysIncluded": []any{
			map[string]any{"doc": "packages/cli/CLAUDE.md", "disposition": "auto-included-fresh"},
			map[string]any{"doc": "docs/CHANGELOG.md", "disposition": "auto-included-fresh"},
			map[string]any{"doc": "CLAUDE.md", "disposition": "skipped-no-user-visible-change"},
		},
		"patches": []any{
			map[string]any{"doc": "packages/cli/CLAUDE.md", "verdict": "applied", "linesChanged": 2},
			map[string]any{"doc": "docs/CHANGELOG.md", "verdict": "applied", "linesChanged": 5},
			map[string]any{"doc": "some/other.md", "verdict": "skipped", "linesChanged": 0},
		},
		"twoPassRun": map[string]any{
			"directRef":    true,
			"conceptLevel": true,
			"mentionsPass": true,
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("updateDocsStepFixture: marshal: %v", err)
	}
	completedAt := "2026-04-29T06:26:53Z"
	return Step{
		StepID:      "STEP_08_UPDATE_DOCS",
		Name:        StepUpdateDocs,
		Status:      StatusCompleted,
		CompletedAt: &completedAt,
		Task:        json.RawMessage(raw),
	}
}

func minimalUpdateDocsStepFixture(t *testing.T) Step {
	t.Helper()
	payload := map[string]any{
		"budgetUsed": 0,
		"budgetMax":  8,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("minimalUpdateDocsStepFixture: marshal: %v", err)
	}
	return Step{
		StepID: "STEP_08_UPDATE_DOCS",
		Name:   StepUpdateDocs,
		Task:   json.RawMessage(raw),
	}
}

func TestRender_UpdateDocs_AllFieldsPresent(t *testing.T) {
	step := updateDocsStepFixture(t)

	out, err := Render(step, "update-docs")
	if err != nil {
		t.Fatalf("Render: unexpected error: %v", err)
	}

	checks := []struct {
		label string
		want  string
	}{
		{"mode", "anchor-only"},
		{"budget used", "8/20"},
		{"budget tier", "large"},
		{"docs mentioning count", "2 source files probed"},
		{"anchor doc", "packages/cli/CLAUDE.md:auto-included-fresh"},
		{"patches count", "3 total"},
		{"applied count", "2 applied"},
		{"skipped count", "1 skipped"},
		{"two-pass directRef", "directRef=true"},
		{"two-pass conceptLevel", "conceptLevel=true"},
		{"two-pass mentionsPass", "mentionsPass=true"},
		{"budget label", "Budget:"},
		{"patches label", "Patches:"},
	}

	for _, c := range checks {
		if !strings.Contains(out, c.want) {
			t.Errorf("[%s] expected output to contain %q\nfull output:\n%s", c.label, c.want, out)
		}
	}
}

func TestRender_UpdateDocs_MissingOptionalFields(t *testing.T) {
	step := minimalUpdateDocsStepFixture(t)

	out, err := Render(step, "update-docs")
	if err != nil {
		t.Fatalf("Render: unexpected error for minimal step: %v", err)
	}

	if !strings.Contains(out, "0/8") {
		t.Errorf("expected budget 0/8\nfull output:\n%s", out)
	}
	if !strings.Contains(out, "n/a") {
		t.Errorf("expected n/a for missing mode\nfull output:\n%s", out)
	}
	if !strings.Contains(out, "0 total") {
		t.Errorf("expected 0 total patches\nfull output:\n%s", out)
	}
}

func TestRender_UpdateDocs_WrongStepType(t *testing.T) {
	step := taskStepFixture(t) // TASK step, not UPDATE_DOCS

	_, err := Render(step, "update-docs")
	if err == nil {
		t.Fatal("expected error for wrong step type, got nil")
	}
	if !strings.Contains(err.Error(), "update-docs") {
		t.Errorf("expected error to mention template name %q, got: %v", "update-docs", err)
	}
	if !strings.Contains(err.Error(), string(StepTask)) {
		t.Errorf("expected error to mention actual step name %q, got: %v", StepTask, err)
	}
}

// ── generate-task fixtures ────────────────────────────────────────────────────

func generateTaskStepFixture(t *testing.T) Step {
	t.Helper()
	payload := map[string]any{
		"totalTasks": 3,
		"tasksOrder": []any{"TASK_01", "TASK_02", "TASK_03"},
		"dependencyGraph": map[string]any{
			"TASK_01": []any{},
			"TASK_02": []any{"TASK_01"},
			"TASK_03": []any{"TASK_01", "TASK_02"},
		},
		"parallelizable":  []any{[]any{"TASK_02", "TASK_03"}},
		"parallelStrategy": "worktree-isolated parallel for independent layers",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("generateTaskStepFixture: marshal: %v", err)
	}
	completedAt := "2026-04-29T04:36:44Z"
	return Step{
		StepID:      "STEP_03_TASKS_MANIFEST",
		Name:        StepTasksManifest,
		Status:      StatusCompleted,
		CompletedAt: &completedAt,
		Task:        json.RawMessage(raw),
	}
}

func minimalGenerateTaskStepFixture(t *testing.T) Step {
	t.Helper()
	payload := map[string]any{
		"totalTasks": 1,
		"tasksOrder": []any{"TASK_01"},
		"dependencyGraph": map[string]any{
			"TASK_01": []any{},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("minimalGenerateTaskStepFixture: marshal: %v", err)
	}
	return Step{
		StepID: "STEP_03_TASKS_MANIFEST",
		Name:   StepTasksManifest,
		Task:   json.RawMessage(raw),
	}
}

func TestRender_GenerateTask_AllFieldsPresent(t *testing.T) {
	step := generateTaskStepFixture(t)

	out, err := Render(step, "generate-task")
	if err != nil {
		t.Fatalf("Render: unexpected error: %v", err)
	}

	checks := []struct {
		label string
		want  string
	}{
		{"total tasks", "Total tasks: 3"},
		{"order", "TASK_01 → TASK_02 → TASK_03"},
		{"dep graph TASK_01 none", "TASK_01 depends on: (none)"},
		{"dep graph TASK_02", "TASK_02 depends on: TASK_01"},
		{"dep graph TASK_03", "TASK_03 depends on: TASK_01, TASK_02"},
		{"parallelizable", "[TASK_02, TASK_03]"},
		{"strategy", "worktree-isolated parallel for independent layers"},
		{"total label", "Total tasks:"},
		{"order label", "Order:"},
		{"dep label", "Dependency graph:"},
	}

	for _, c := range checks {
		if !strings.Contains(out, c.want) {
			t.Errorf("[%s] expected output to contain %q\nfull output:\n%s", c.label, c.want, out)
		}
	}
}

func TestRender_GenerateTask_MissingOptionalFields(t *testing.T) {
	step := minimalGenerateTaskStepFixture(t)

	out, err := Render(step, "generate-task")
	if err != nil {
		t.Fatalf("Render: unexpected error for minimal step: %v", err)
	}

	if !strings.Contains(out, "Total tasks: 1") {
		t.Errorf("expected total tasks 1\nfull output:\n%s", out)
	}
	if !strings.Contains(out, "(sequential)") {
		t.Errorf("expected (sequential) for empty parallelizable\nfull output:\n%s", out)
	}
	if !strings.Contains(out, "default") {
		t.Errorf("expected default strategy for missing parallelStrategy\nfull output:\n%s", out)
	}
}

func TestRender_GenerateTask_WrongStepType(t *testing.T) {
	step := brainstormingStepFixture(t) // BRAINSTORMING step, not TASKS_MANIFEST

	_, err := Render(step, "generate-task")
	if err == nil {
		t.Fatal("expected error for wrong step type, got nil")
	}
	if !strings.Contains(err.Error(), "generate-task") {
		t.Errorf("expected error to mention template name %q, got: %v", "generate-task", err)
	}
	if !strings.Contains(err.Error(), string(StepBrainstorming)) {
		t.Errorf("expected error to mention actual step name %q, got: %v", StepBrainstorming, err)
	}
}
