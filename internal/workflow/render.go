package workflow

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Render produces a pre-formatted text block for a named template, suitable
// for embedding directly in agent dispatch prompts.
//
// Templates are tied to step types: "execute-task" expects a TASK step.
// Calling a template with the wrong step type returns an error.
func Render(step Step, template string) (string, error) {
	switch template {
	case "execute-task":
		return renderExecuteTask(step)
	case "code-review":
		return renderCodeReview(step)
	case "brainstorming":
		return renderBrainstorming(step)
	case "update-docs":
		return renderUpdateDocs(step)
	case "generate-task":
		return renderGenerateTask(step)
	case "task-context":
		return renderTaskContext(step)
	case "task-evidence":
		return renderTaskEvidence(step)
	case "finding":
		return renderFinding(step, "")
	default:
		return "", fmt.Errorf("unknown template %q (known: execute-task, code-review, brainstorming, update-docs, generate-task, task-context, task-evidence, finding)", template)
	}
}

// taskPayload is the decoded shape of a TASK step's .task field.
// Only the fields relevant to the execute-task template are declared; the
// rest are decoded into the catch-all map via custom unmarshalling.
type taskPayload struct {
	Title          string         `json:"title"`
	Scope          []string       `json:"scope"`
	SuggestedModel string         `json:"suggestedModel"`
	Trivial        bool           `json:"trivial"`
	Invariants     []taskInvariant `json:"invariants"`
	Reviewer       taskReviewer   `json:"reviewer"`
	Explorer       taskExplorer   `json:"explorer"`
}

type taskInvariant struct {
	Rule   string `json:"rule"`
	Source string `json:"source"`
}

type taskReviewer struct {
	TDDDecision taskTDDDecision `json:"tddDecision"`
	TestSpecs   []taskTestSpec  `json:"testSpecs"`
}

type taskTDDDecision struct {
	Applicable bool   `json:"applicable"`
	Reason     string `json:"reason"`
}

type taskTestSpec struct {
	Type string `json:"type"`
}

type taskExplorer struct {
	SkillsFound []taskSkillEntry `json:"skillsFound"`
}

type taskSkillEntry struct {
	Skill  string `json:"skill"`
	Domain string `json:"domain"`
}

// ── code-review ───────────────────────────────────────────────────────────────

type codeReviewScope struct {
	Files int    `json:"files"`
	Lines int    `json:"lines"`
	Tier  string `json:"tier"`
}

type codeReviewTeammate struct {
	Name string `json:"name"`
	Lane string `json:"lane"`
}

type codeReviewAgentTeam struct {
	Teammates  []codeReviewTeammate `json:"teammates"`
	RoundTrips int                  `json:"roundTrips"`
}

type codeReviewFinding struct {
	ID          string `json:"id"`
	Severity    string `json:"severity"`
	Description string `json:"description"`
}

type codeReviewSeverityCounts struct {
	High   int `json:"high"`
	Medium int `json:"medium"`
	Low    int `json:"low"`
}

type codeReviewPayload struct {
	Mode           string                   `json:"mode"`
	Tier           string                   `json:"tier"`
	Scope          codeReviewScope          `json:"scope"`
	AgentTeam      codeReviewAgentTeam      `json:"agentTeam"`
	Findings       []codeReviewFinding      `json:"findings"`
	SeverityCounts codeReviewSeverityCounts `json:"severityCounts"`
	Themes         []string                 `json:"themes"`
}

func renderCodeReview(step Step) (string, error) {
	if step.Name != StepCodeReview {
		return "", fmt.Errorf("code-review template requires a CODE_REVIEW step, got %q", step.Name)
	}

	var p codeReviewPayload
	if len(step.Task) > 0 {
		if err := json.Unmarshal(step.Task, &p); err != nil {
			return "", fmt.Errorf("code-review: decode payload: %w", err)
		}
	}

	var b strings.Builder

	mode := p.Mode
	if mode == "" {
		mode = "n/a"
	}
	fmt.Fprintf(&b, "Mode: %s\n", mode)
	fmt.Fprintf(&b, "Tier: %s\n", p.Tier)
	fmt.Fprintf(&b, "Scope: %d files / %d lines / tier %s\n", p.Scope.Files, p.Scope.Lines, p.Scope.Tier)

	// Reviewers
	if len(p.AgentTeam.Teammates) == 0 {
		fmt.Fprintf(&b, "Reviewers: (none)\n")
	} else {
		parts := make([]string, 0, len(p.AgentTeam.Teammates))
		for _, tm := range p.AgentTeam.Teammates {
			parts = append(parts, tm.Name+" "+tm.Lane)
		}
		fmt.Fprintf(&b, "Reviewers: %s\n", strings.Join(parts, ", "))
	}

	fmt.Fprintf(&b, "Findings: %d total (%dH/%dM/%dL)\n",
		len(p.Findings),
		p.SeverityCounts.High,
		p.SeverityCounts.Medium,
		p.SeverityCounts.Low,
	)
	fmt.Fprintf(&b, "Round-trips: %d\n", p.AgentTeam.RoundTrips)

	// Top-priority highs (first 5)
	var highs []codeReviewFinding
	for _, f := range p.Findings {
		if f.Severity == "high" {
			highs = append(highs, f)
			if len(highs) == 5 {
				break
			}
		}
	}
	if len(highs) == 0 {
		fmt.Fprintf(&b, "Top-priority highs: (none)\n")
	} else {
		fmt.Fprintf(&b, "Top-priority highs:\n")
		for _, f := range highs {
			fmt.Fprintf(&b, "  - %s: %s\n", f.ID, f.Description)
		}
	}

	// Themes
	if len(p.Themes) == 0 {
		fmt.Fprintf(&b, "Themes: (none)\n")
	} else {
		fmt.Fprintf(&b, "Themes: %s\n", strings.Join(p.Themes, "; "))
	}

	return b.String(), nil
}

// ── brainstorming ─────────────────────────────────────────────────────────────

type brainstormPayload struct {
	PrimaryUser        string   `json:"primaryUser"`
	JobToBeDone        string   `json:"jobToBeDone"`
	SuccessSignal      string   `json:"successSignal"`
	InScope            []string `json:"inScope"`
	OutOfScope         []string `json:"outOfScope"`
	RepoSurface        []string `json:"repoSurface"`
	OpenQuestions      []string `json:"openQuestions"`
	Assumptions        []string `json:"assumptions"`
	AcceptanceCriteria []string `json:"acceptanceCriteria"`
}

func renderBrainstorming(step Step) (string, error) {
	if step.Name != StepBrainstorming {
		return "", fmt.Errorf("brainstorming template requires a BRAINSTORMING step, got %q", step.Name)
	}

	var p brainstormPayload
	if len(step.Task) > 0 {
		if err := json.Unmarshal(step.Task, &p); err != nil {
			return "", fmt.Errorf("brainstorming: decode payload: %w", err)
		}
	}

	var b strings.Builder

	primaryUser := p.PrimaryUser
	if primaryUser == "" {
		primaryUser = "(none)"
	}
	fmt.Fprintf(&b, "Primary user: %s\n", primaryUser)

	jobToBeDone := p.JobToBeDone
	if jobToBeDone == "" {
		jobToBeDone = "(none)"
	}
	fmt.Fprintf(&b, "Job-to-be-done: %s\n", jobToBeDone)

	successSignal := p.SuccessSignal
	if successSignal == "" {
		successSignal = "(none)"
	}
	fmt.Fprintf(&b, "Success signal: %s\n", successSignal)

	if len(p.InScope) == 0 {
		fmt.Fprintf(&b, "In scope: (none)\n")
	} else {
		fmt.Fprintf(&b, "In scope: %s\n", strings.Join(p.InScope, ", "))
	}

	if len(p.OutOfScope) == 0 {
		fmt.Fprintf(&b, "Out of scope: (none)\n")
	} else {
		fmt.Fprintf(&b, "Out of scope: %s\n", strings.Join(p.OutOfScope, ", "))
	}

	if len(p.RepoSurface) == 0 {
		fmt.Fprintf(&b, "Repo surface: (none)\n")
	} else {
		fmt.Fprintf(&b, "Repo surface: %s\n", strings.Join(p.RepoSurface, ", "))
	}

	if len(p.OpenQuestions) == 0 {
		fmt.Fprintf(&b, "Open questions: (none)\n")
	} else {
		fmt.Fprintf(&b, "Open questions:\n")
		for _, q := range p.OpenQuestions {
			fmt.Fprintf(&b, "  - %s\n", q)
		}
	}

	if len(p.Assumptions) == 0 {
		fmt.Fprintf(&b, "Assumptions: (none)\n")
	} else {
		fmt.Fprintf(&b, "Assumptions:\n")
		for _, a := range p.Assumptions {
			fmt.Fprintf(&b, "  - %s\n", a)
		}
	}

	fmt.Fprintf(&b, "Acceptance criteria: %d declared\n", len(p.AcceptanceCriteria))

	return b.String(), nil
}

// ── update-docs ───────────────────────────────────────────────────────────────

type updateDocsAnchor struct {
	Doc         string `json:"doc"`
	Disposition string `json:"disposition"`
}

type updateDocsPatch struct {
	Verdict string `json:"verdict"`
}

type updateDocsTwoPassRun struct {
	DirectRef    bool `json:"directRef"`
	ConceptLevel bool `json:"conceptLevel"`
	MentionsPass bool `json:"mentionsPass"`
}

type updateDocsPayload struct {
	Mode                     string               `json:"mode"`
	BudgetUsed               int                  `json:"budgetUsed"`
	BudgetMax                int                  `json:"budgetMax"`
	BudgetTier               string               `json:"budgetTier"`
	DocsMentioning           []json.RawMessage    `json:"docsMentioning"`
	AnchorDocsAlwaysIncluded []updateDocsAnchor   `json:"anchorDocsAlwaysIncluded"`
	Patches                  []updateDocsPatch    `json:"patches"`
	TwoPassRun               updateDocsTwoPassRun `json:"twoPassRun"`
}

func renderUpdateDocs(step Step) (string, error) {
	if step.Name != StepUpdateDocs {
		return "", fmt.Errorf("update-docs template requires a UPDATE_DOCS step, got %q", step.Name)
	}

	var p updateDocsPayload
	if len(step.Task) > 0 {
		if err := json.Unmarshal(step.Task, &p); err != nil {
			return "", fmt.Errorf("update-docs: decode payload: %w", err)
		}
	}

	var b strings.Builder

	mode := p.Mode
	if mode == "" {
		mode = "n/a"
	}
	fmt.Fprintf(&b, "Mode: %s\n", mode)
	fmt.Fprintf(&b, "Budget: %d/%d (tier %s)\n", p.BudgetUsed, p.BudgetMax, p.BudgetTier)
	fmt.Fprintf(&b, "Docs mentioned: %d source files probed\n", len(p.DocsMentioning))

	// Anchor docs
	if len(p.AnchorDocsAlwaysIncluded) == 0 {
		fmt.Fprintf(&b, "Anchor docs: (none)\n")
	} else {
		parts := make([]string, 0, len(p.AnchorDocsAlwaysIncluded))
		for _, a := range p.AnchorDocsAlwaysIncluded {
			parts = append(parts, a.Doc+":"+a.Disposition)
		}
		fmt.Fprintf(&b, "Anchor docs: %s\n", strings.Join(parts, ", "))
	}

	// Patch counts
	var applied, skipped, failed int
	for _, patch := range p.Patches {
		switch patch.Verdict {
		case "applied":
			applied++
		case "skipped":
			skipped++
		case "failed":
			failed++
		}
	}
	fmt.Fprintf(&b, "Patches: %d total (%d applied, %d skipped, %d failed)\n",
		len(p.Patches), applied, skipped, failed)

	// Two-pass run
	fmt.Fprintf(&b, "Two-pass run: directRef=%v conceptLevel=%v mentionsPass=%v\n",
		p.TwoPassRun.DirectRef,
		p.TwoPassRun.ConceptLevel,
		p.TwoPassRun.MentionsPass,
	)

	return b.String(), nil
}

// ── generate-task ─────────────────────────────────────────────────────────────

type tasksManifestPayload struct {
	TotalTasks       int                 `json:"totalTasks"`
	TasksOrder       []string            `json:"tasksOrder"`
	DependencyGraph  map[string][]string `json:"dependencyGraph"`
	Parallelizable   [][]string          `json:"parallelizable"`
	ParallelStrategy string              `json:"parallelStrategy"`
}

func renderGenerateTask(step Step) (string, error) {
	if step.Name != StepTasksManifest {
		return "", fmt.Errorf("generate-task template requires a TASKS_MANIFEST step, got %q", step.Name)
	}

	var p tasksManifestPayload
	if len(step.Task) > 0 {
		if err := json.Unmarshal(step.Task, &p); err != nil {
			return "", fmt.Errorf("generate-task: decode payload: %w", err)
		}
	}

	var b strings.Builder

	fmt.Fprintf(&b, "Total tasks: %d\n", p.TotalTasks)

	if len(p.TasksOrder) == 0 {
		fmt.Fprintf(&b, "Order: (none)\n")
	} else {
		fmt.Fprintf(&b, "Order: %s\n", strings.Join(p.TasksOrder, " → "))
	}

	// Dependency graph — iterate in tasksOrder for deterministic output
	fmt.Fprintf(&b, "Dependency graph:\n")
	for _, taskID := range p.TasksOrder {
		deps, ok := p.DependencyGraph[taskID]
		if !ok || len(deps) == 0 {
			fmt.Fprintf(&b, "  %s depends on: (none)\n", taskID)
		} else {
			fmt.Fprintf(&b, "  %s depends on: %s\n", taskID, strings.Join(deps, ", "))
		}
	}

	// Parallelizable groups
	if len(p.Parallelizable) == 0 {
		fmt.Fprintf(&b, "Parallelizable groups: (sequential)\n")
	} else {
		groups := make([]string, 0, len(p.Parallelizable))
		for _, group := range p.Parallelizable {
			groups = append(groups, "["+strings.Join(group, ", ")+"]")
		}
		fmt.Fprintf(&b, "Parallelizable groups: %s\n", strings.Join(groups, " | "))
	}

	strategy := p.ParallelStrategy
	if strategy == "" {
		strategy = "default"
	}
	fmt.Fprintf(&b, "Strategy: %s\n", strategy)

	return b.String(), nil
}

func renderExecuteTask(step Step) (string, error) {
	if step.Name != StepTask {
		return "", fmt.Errorf("execute-task template requires a TASK step, got %q", step.Name)
	}

	var tp taskPayload
	if len(step.Task) > 0 {
		if err := json.Unmarshal(step.Task, &tp); err != nil {
			return "", fmt.Errorf("execute-task: decode task payload: %w", err)
		}
	}

	var b strings.Builder

	// Title
	title := tp.Title
	if title == "" {
		title = "(none)"
	}
	fmt.Fprintf(&b, "Title: %s\n", title)

	// Scope
	if len(tp.Scope) == 0 {
		fmt.Fprintf(&b, "Scope: (none)\n")
	} else {
		fmt.Fprintf(&b, "Scope: %s\n", strings.Join(tp.Scope, ", "))
	}

	// TDD applicable
	if tp.Reviewer.TDDDecision.Applicable {
		fmt.Fprintf(&b, "TDD applicable: yes\n")
		if tp.Reviewer.TDDDecision.Reason != "" {
			fmt.Fprintf(&b, "TDD reason: %s\n", tp.Reviewer.TDDDecision.Reason)
		}
	} else {
		fmt.Fprintf(&b, "TDD applicable: no\n")
	}

	// Test specs
	var redCount, greenCount int
	for _, ts := range tp.Reviewer.TestSpecs {
		switch ts.Type {
		case "red":
			redCount++
		case "green":
			greenCount++
		}
	}
	fmt.Fprintf(&b, "Test specs: %d red, %d green\n", redCount, greenCount)

	// Skills to invoke
	if len(tp.Explorer.SkillsFound) == 0 {
		fmt.Fprintf(&b, "Skills: (none)\n")
	} else {
		skills := make([]string, 0, len(tp.Explorer.SkillsFound))
		for _, sf := range tp.Explorer.SkillsFound {
			skills = append(skills, sf.Skill)
		}
		fmt.Fprintf(&b, "Skills: %s\n", strings.Join(skills, ", "))
	}

	// Invariants
	if len(tp.Invariants) == 0 {
		fmt.Fprintf(&b, "Invariants: (none)\n")
	} else {
		fmt.Fprintf(&b, "Invariants:\n")
		for _, inv := range tp.Invariants {
			fmt.Fprintf(&b, "  - %s (source: %s)\n", inv.Rule, inv.Source)
		}
	}

	// Suggested model
	model := tp.SuggestedModel
	if model == "" {
		model = "sonnet"
	}
	fmt.Fprintf(&b, "Suggested model: %s\n", model)

	// Trivial
	trivial := "no"
	if tp.Trivial {
		trivial = "yes"
	}
	fmt.Fprintf(&b, "Trivial: %s\n", trivial)

	return b.String(), nil
}

// ── task-context ──────────────────────────────────────────────────────────────
//
// Compact TASK step summary for code-review reviewers and feature-acceptance
// Phase 2.3. Stays under ~150 tokens.

type taskACEntry struct {
	ID string `json:"id"`
	FR string `json:"fr"`
}

type taskContextPayload struct {
	Title              string         `json:"title"`
	Scope              []string       `json:"scope"`
	Invariants         []taskInvariant `json:"invariants"`
	AcceptanceCriteria []taskACEntry  `json:"acceptanceCriteria"`
	Explorer           taskExplorer   `json:"explorer"`
}

func renderTaskContext(step Step) (string, error) {
	if step.Name != StepTask {
		return "", fmt.Errorf("task-context template requires a TASK step, got %q", step.Name)
	}

	var tp taskContextPayload
	if len(step.Task) > 0 {
		if err := json.Unmarshal(step.Task, &tp); err != nil {
			return "", fmt.Errorf("task-context: decode task payload: %w", err)
		}
	}

	var b strings.Builder

	// Title
	title := tp.Title
	if title == "" {
		title = "(none)"
	}
	fmt.Fprintf(&b, "Task: %s\n", title)

	// Scope
	n := len(tp.Scope)
	if n == 0 {
		fmt.Fprintf(&b, "Scope (0 files): —\n")
	} else {
		fmt.Fprintf(&b, "Scope (%d files): %s\n", n, strings.Join(tp.Scope, "; "))
	}

	// Invariants
	if len(tp.Invariants) == 0 {
		fmt.Fprintf(&b, "Invariants: (none)\n")
	} else {
		fmt.Fprintf(&b, "Invariants:\n")
		for _, inv := range tp.Invariants {
			fmt.Fprintf(&b, "  - %s\n", inv.Rule)
		}
	}

	// AC binds
	if len(tp.AcceptanceCriteria) == 0 {
		fmt.Fprintf(&b, "AC binds: (none)\n")
	} else {
		pairs := make([]string, 0, len(tp.AcceptanceCriteria))
		for _, ac := range tp.AcceptanceCriteria {
			if ac.FR != "" {
				pairs = append(pairs, ac.ID+" → "+ac.FR)
			} else {
				pairs = append(pairs, ac.ID)
			}
		}
		fmt.Fprintf(&b, "AC binds: %s\n", strings.Join(pairs, ", "))
	}

	// Skills
	if len(tp.Explorer.SkillsFound) == 0 {
		fmt.Fprintf(&b, "Skills: (none)\n")
	} else {
		skills := make([]string, 0, len(tp.Explorer.SkillsFound))
		for _, sf := range tp.Explorer.SkillsFound {
			skills = append(skills, sf.Skill)
		}
		fmt.Fprintf(&b, "Skills: %s\n", strings.Join(skills, ", "))
	}

	return b.String(), nil
}

// ── task-evidence ─────────────────────────────────────────────────────────────
//
// TASK step execution evidence for feature-acceptance Phase 2.3.

type taskExecutionGates struct {
	LintBaseline     string `json:"lintBaseline"`
	LintPost         string `json:"lintPost"`
	TypecheckBaseline string `json:"typecheckBaseline"`
	TypecheckPost    string `json:"typecheckPost"`
	TestsBaseline    string `json:"testsBaseline"`
	TestsPost        string `json:"testsPost"`
}

type taskExecutionRegression struct {
	File    string `json:"file"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

type taskExecutionScopeAdj struct {
	Kind string `json:"kind"`
	Note string `json:"note"`
}

type taskExecution struct {
	FilesCreated      []string                  `json:"filesCreated"`
	FilesModified     []string                  `json:"filesModified"`
	FilesDeleted      []string                  `json:"filesDeleted"`
	Gates             taskExecutionGates        `json:"gates"`
	Regressions       []taskExecutionRegression `json:"regressions"`
	ScopeAdjustments  []taskExecutionScopeAdj   `json:"scopeAdjustments"`
}

type taskEvidencePayload struct {
	Execution taskExecution `json:"execution"`
}

func renderTaskEvidence(step Step) (string, error) {
	if step.Name != StepTask {
		return "", fmt.Errorf("task-evidence template requires a TASK step, got %q", step.Name)
	}

	var tp taskEvidencePayload
	if len(step.Task) > 0 {
		if err := json.Unmarshal(step.Task, &tp); err != nil {
			return "", fmt.Errorf("task-evidence: decode task payload: %w", err)
		}
	}

	var b strings.Builder
	ex := tp.Execution

	fmt.Fprintf(&b, "Status: %s\n", step.Status)

	fmt.Fprintf(&b, "Files: created %d, modified %d, deleted %d\n",
		len(ex.FilesCreated), len(ex.FilesModified), len(ex.FilesDeleted))

	g := ex.Gates
	lb := g.LintBaseline
	if lb == "" {
		lb = "n/a"
	}
	lp := g.LintPost
	if lp == "" {
		lp = "n/a"
	}
	tb := g.TypecheckBaseline
	if tb == "" {
		tb = "n/a"
	}
	tp2 := g.TypecheckPost
	if tp2 == "" {
		tp2 = "n/a"
	}
	tsb := g.TestsBaseline
	if tsb == "" {
		tsb = "n/a"
	}
	tsp := g.TestsPost
	if tsp == "" {
		tsp = "n/a"
	}
	fmt.Fprintf(&b, "Gates: lint %s→%s · typecheck %s→%s · tests %s→%s\n",
		lb, lp, tb, tp2, tsb, tsp)

	if len(ex.Regressions) == 0 {
		fmt.Fprintf(&b, "Regression: none\n")
	} else {
		parts := make([]string, 0, len(ex.Regressions))
		for _, r := range ex.Regressions {
			parts = append(parts, r.File+":"+r.Type+":"+r.Message)
		}
		fmt.Fprintf(&b, "Regression: %s\n", strings.Join(parts, "; "))
	}

	adj := len(ex.ScopeAdjustments)
	if adj == 0 {
		fmt.Fprintf(&b, "Scope adjustments: 0\n")
	} else {
		notes := make([]string, 0, len(ex.ScopeAdjustments))
		for _, a := range ex.ScopeAdjustments {
			notes = append(notes, a.Kind+": "+a.Note)
		}
		fmt.Fprintf(&b, "Scope adjustments: %d (%s)\n", adj, strings.Join(notes, "; "))
	}

	return b.String(), nil
}

// ── finding ───────────────────────────────────────────────────────────────────
//
// Emit a single finding from a CODE_REVIEW step's findings[] array.
//
// Design decision on finding selector:
//   - The public Render() entry-point ("finding" case) calls renderFinding with
//     an empty findingID, which returns the first open (non-"closed") finding.
//   - Callers that need a specific finding call RenderFinding(step, id) directly.
//   - The get-step cobra command reads a --finding flag and passes it via
//     RenderFinding when --render finding is requested.
//
// This keeps the Render() interface uniform (single-string template name) while
// giving the cobra layer a clean API for the ID selector without bolting extra
// state onto the template name string.

// RenderFinding is like Render("finding", ...) but accepts an explicit finding
// ID. When findingID is "", it returns the first open finding.
func RenderFinding(step Step, findingID string) (string, error) {
	return renderFinding(step, findingID)
}

type findingEntry struct {
	ID            string `json:"id"`
	Severity      string `json:"severity"`
	Category      string `json:"category"`
	File          string `json:"file"`
	Line          int    `json:"line"`
	Domain        string `json:"domain"`
	Description   string `json:"description"`
	SuggestedFix  string `json:"suggestedFix"`
	AssignedSkill string `json:"assignedSkill"`
	Status        string `json:"status"`
}

type findingPayload struct {
	Findings []findingEntry `json:"findings"`
}

func renderFinding(step Step, findingID string) (string, error) {
	if step.Name != StepCodeReview {
		return "", fmt.Errorf("finding template requires a CODE_REVIEW step, got %q", step.Name)
	}

	var p findingPayload
	if len(step.Task) > 0 {
		if err := json.Unmarshal(step.Task, &p); err != nil {
			return "", fmt.Errorf("finding: decode payload: %w", err)
		}
	}

	if len(p.Findings) == 0 {
		return "", fmt.Errorf("finding: CODE_REVIEW step %q has no findings", step.StepID)
	}

	// Select finding by ID, or first open finding.
	var selected *findingEntry
	for i := range p.Findings {
		f := &p.Findings[i]
		if findingID != "" {
			if f.ID == findingID {
				selected = f
				break
			}
		} else {
			// First open (not "closed") finding.
			if f.Status != "closed" {
				selected = f
				break
			}
		}
	}
	// Fallback: if all are closed or exact ID not found, use first entry.
	if selected == nil {
		selected = &p.Findings[0]
	}

	var b strings.Builder

	sev := selected.Severity
	if sev == "" {
		sev = "n/a"
	}
	cat := selected.Category
	if cat == "" {
		cat = "n/a"
	}
	fmt.Fprintf(&b, "Finding %s (severity: %s, category: %s)\n", selected.ID, sev, cat)

	fileLine := selected.File
	if fileLine == "" {
		fileLine = "n/a"
	} else if selected.Line > 0 {
		fileLine = fmt.Sprintf("%s:%d", selected.File, selected.Line)
	}
	fmt.Fprintf(&b, "File: %s\n", fileLine)

	domain := selected.Domain
	if domain == "" {
		domain = "n/a"
	}
	fmt.Fprintf(&b, "Domain: %s\n", domain)

	desc := selected.Description
	if desc == "" {
		desc = "(none)"
	}
	fmt.Fprintf(&b, "Description: %s\n", desc)

	fix := selected.SuggestedFix
	if fix == "" {
		fix = "(none)"
	}
	fmt.Fprintf(&b, "Suggested fix: %s\n", fix)

	skill := selected.AssignedSkill
	if skill == "" {
		skill = "(none)"
	}
	fmt.Fprintf(&b, "AssignedSkill: %s\n", skill)

	return b.String(), nil
}
