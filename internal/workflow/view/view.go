// Package view assembles the rich, LLM-context-friendly projection of a
// single workflow step. It is the data layer behind `browzer get-step`.
//
// The exported StepView matches the `#StepView` definition in
// schemas/workflow-v1.cue.
package view

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// StepView is the consolidated, LLM-friendly projection. Mirrors the CUE
// `#StepView` definition. JSON tags are part of the public contract.
type StepView struct {
	StepID         string         `json:"stepId"`
	Phase          string         `json:"phase"`
	Status         string         `json:"status"`
	Role           string         `json:"role,omitempty"`
	SkillsToInvoke []string       `json:"skillsToInvoke,omitempty"`
	Scope          map[string]any `json:"scope,omitempty"`
	PRDContext     map[string]any `json:"prdContext,omitempty"`
	Deps           map[string]any `json:"deps,omitempty"`
	Invariants     []string       `json:"invariants,omitempty"`
	DoneWhen       []string       `json:"doneWhen,omitempty"`
	// Body is the raw step-type-specific payload (e.g. PRD body, task body).
	// Carried along so templates can render type-specific sections.
	Body map[string]any `json:"body,omitempty"`
	// Tasks is the projected list of task briefs (TASKS phase only).
	Tasks []map[string]any `json:"tasks,omitempty"`
	// OriginalRequest + BrainstormSummary are denormalised onto PRD views.
	OriginalRequest   string `json:"originalRequest,omitempty"`
	BrainstormSummary string `json:"brainstormSummary,omitempty"`
}

// Build inspects the raw workflow.json map and assembles a StepView for the
// requested phase. Returns an error (suitable for exit-code 2) when the
// phase is unknown or no matching step exists.
func Build(raw map[string]any, phase string) (StepView, error) {
	phase = strings.TrimSpace(phase)
	if phase == "" {
		return StepView{}, fmt.Errorf("phase is required")
	}

	steps := stepsOf(raw)

	// PHASE → step lookup
	var step map[string]any
	var stepName string

	switch {
	case phase == "PRD":
		step = findByName(steps, "PRD")
		stepName = "PRD"
	case phase == "TASKS" || phase == "TASKS_MANIFEST":
		step = findByName(steps, "TASKS_MANIFEST")
		stepName = "TASKS"
	case strings.HasPrefix(phase, "TASK_"):
		step = findByTaskID(steps, phase)
		if step == nil {
			step = findByStepID(steps, phase)
		}
		stepName = "TASK"
	case phase == "BRAINSTORM" || phase == "BRAINSTORMING":
		step = findByName(steps, "BRAINSTORMING")
		stepName = "BRAINSTORMING"
	case phase == "ORIGINAL_REQUEST":
		view := StepView{Phase: "ORIGINAL_REQUEST"}
		if s, ok := raw["originalRequest"].(string); ok {
			view.OriginalRequest = s
		}
		if view.OriginalRequest == "" {
			return StepView{}, fmt.Errorf("get-step: no originalRequest recorded on this workflow")
		}
		return view, nil
	case phase == "CONFIG":
		view := StepView{Phase: "CONFIG"}
		if cfg, ok := raw["config"].(map[string]any); ok {
			view.Body = cfg
		}
		if view.Body == nil {
			return StepView{}, fmt.Errorf("get-step: no config block on this workflow")
		}
		return view, nil
	case phase == "CODE_REVIEW",
		phase == "RECEIVING_CODE_REVIEW",
		phase == "WRITE_TESTS",
		phase == "UPDATE_DOCS",
		phase == "FEATURE_ACCEPTANCE",
		phase == "COMMIT":
		step = findByName(steps, phase)
		stepName = phase
	default:
		return StepView{}, fmt.Errorf("get-step: unknown phase %q", phase)
	}

	if step == nil {
		return StepView{}, fmt.Errorf("get-step: no step found for phase %q", phase)
	}

	view := StepView{
		Phase:  stepName,
		StepID: stringField(step, "stepId"),
		Status: stringField(step, "status"),
	}
	if v, ok := step["skillsToInvoke"].([]any); ok {
		view.SkillsToInvoke = stringSlice(v)
	}

	switch stepName {
	case "PRD":
		buildPRDView(&view, step, raw)
	case "TASKS":
		buildTasksView(&view, step)
	case "TASK":
		buildTaskView(&view, step, raw)
	default:
		// Generic phase: hand the body to the template untouched. Body key
		// is the lowerCamel of the step name (e.g. "codeReview").
		body := payloadFor(step, stepName)
		if body != nil {
			view.Body = body
		}
	}

	return view, nil
}

func buildPRDView(v *StepView, step, raw map[string]any) {
	body := payloadFor(step, "PRD")
	if body != nil {
		v.Body = body
	}
	if s, ok := raw["originalRequest"].(string); ok {
		v.OriginalRequest = s
	}
	// Pull a one-liner from the brainstorming step, if present.
	for _, s := range stepsOf(raw) {
		if stringField(s, "name") == "BRAINSTORMING" {
			if b, ok := s["brainstorm"].(map[string]any); ok {
				if dec, ok := b["decision"].(map[string]any); ok {
					if sum, ok := dec["summary"].(string); ok {
						v.BrainstormSummary = sum
					}
				}
			}
		}
	}
}

func buildTasksView(v *StepView, step map[string]any) {
	body := payloadFor(step, "TASKS_MANIFEST")
	if body == nil {
		return
	}
	v.Body = body
	if tasks, ok := body["tasks"].([]any); ok {
		for _, t := range tasks {
			if tm, ok := t.(map[string]any); ok {
				brief := map[string]any{
					"id":     tm["id"],
					"title":  tm["title"],
					"status": tm["status"],
				}
				if scope, ok := tm["scope"].(map[string]any); ok {
					brief["domain"] = scope["domain"]
					if files, ok := scope["files"].([]any); ok {
						brief["fileCount"] = len(files)
					}
				}
				v.Tasks = append(v.Tasks, brief)
			}
		}
	}
}

func buildTaskView(v *StepView, step, raw map[string]any) {
	body := payloadFor(step, "TASK")
	if body == nil {
		return
	}
	v.Body = body
	v.Role = stringField(body, "role")
	if scope, ok := body["scope"].(map[string]any); ok {
		v.Scope = scope
	}
	if dw, ok := body["doneWhen"].([]any); ok {
		v.DoneWhen = stringSlice(dw)
	}
	// PRD slice filtered by linkedAcs[].
	if linked, ok := body["linkedAcs"].([]any); ok && len(linked) > 0 {
		ids := map[string]struct{}{}
		for _, x := range linked {
			if s, ok := x.(string); ok {
				ids[s] = struct{}{}
			}
		}
		for _, s := range stepsOf(raw) {
			if stringField(s, "name") != "PRD" {
				continue
			}
			prd := payloadFor(s, "PRD")
			if prd == nil {
				continue
			}
			ctx := map[string]any{}
			if acs, ok := prd["acceptanceCriteria"].([]any); ok {
				var filtered []any
				for _, ac := range acs {
					if am, ok := ac.(map[string]any); ok {
						if id, ok := am["id"].(string); ok {
							if _, hit := ids[id]; hit {
								filtered = append(filtered, am)
							}
						}
					}
				}
				if len(filtered) > 0 {
					ctx["acceptanceCriteria"] = filtered
				}
			}
			if len(ctx) > 0 {
				v.PRDContext = ctx
			}
			break
		}
	}
	// Skills from explorer.
	if expl, ok := body["explorer"].(map[string]any); ok {
		if found, ok := expl["skillsFound"].([]any); ok {
			var names []string
			for _, sf := range found {
				if sm, ok := sf.(map[string]any); ok {
					if n, ok := sm["name"].(string); ok {
						names = append(names, n)
					}
				} else if s, ok := sf.(string); ok {
					names = append(names, s)
				}
			}
			if len(names) > 0 {
				// Merge with existing SkillsToInvoke, preferring explorer names.
				v.SkillsToInvoke = mergeStrings(v.SkillsToInvoke, names)
			}
		}
	}
	// Invariants from CLAUDE.md (best-effort, repo-rooted).
	v.Invariants = loadInvariantsSnippet()
}

// loadInvariantsSnippet reads <cwd>/CLAUDE.md and extracts the
// "Cross-cutting invariants" bullet list, when present. Best-effort: any
// IO error returns nil.
func loadInvariantsSnippet() []string {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	// Walk up looking for CLAUDE.md (capped to 6 levels).
	dir := cwd
	var path string
	for i := 0; i < 6; i++ {
		p := filepath.Join(dir, "CLAUDE.md")
		if _, err := os.Stat(p); err == nil {
			path = p
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return extractInvariants(string(data))
}

// invariantsHeader matches the section header we want to capture from
// CLAUDE.md.
var invariantsHeader = regexp.MustCompile(`(?i)^##+ +cross[- ]cutting invariants`)

func extractInvariants(md string) []string {
	lines := strings.Split(md, "\n")
	var inSection bool
	var out []string
	for _, ln := range lines {
		if !inSection {
			if invariantsHeader.MatchString(ln) {
				inSection = true
			}
			continue
		}
		// End of section: next header.
		if strings.HasPrefix(ln, "## ") {
			break
		}
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			out = append(out, strings.TrimSpace(trimmed[2:]))
		}
	}
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

// stepsOf returns the steps array as []map[string]any.
func stepsOf(raw map[string]any) []map[string]any {
	stepsRaw, _ := raw["steps"].([]any)
	out := make([]map[string]any, 0, len(stepsRaw))
	for _, s := range stepsRaw {
		if m, ok := s.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func findByStepID(steps []map[string]any, id string) map[string]any {
	for _, s := range steps {
		if stringField(s, "stepId") == id {
			return s
		}
	}
	return nil
}

func findByTaskID(steps []map[string]any, taskID string) map[string]any {
	for _, s := range steps {
		if stringField(s, "taskId") == taskID && stringField(s, "name") == "TASK" {
			return s
		}
	}
	return nil
}

func findByName(steps []map[string]any, name string) map[string]any {
	for _, s := range steps {
		if stringField(s, "name") == name {
			return s
		}
	}
	return nil
}

func stringField(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func stringSlice(in []any) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func mergeStrings(a, b []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, x := range a {
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	for _, x := range b {
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	return out
}

// payloadFor returns the step-type payload map. Each step-name has its own
// canonical key; we fall back to "task" for legacy compat.
func payloadFor(step map[string]any, stepName string) map[string]any {
	key := payloadKey(stepName)
	if key != "" {
		if m, ok := step[key].(map[string]any); ok {
			return m
		}
	}
	if m, ok := step["task"].(map[string]any); ok {
		return m
	}
	return nil
}

// PayloadKey returns the canonical JSON key for a step-name's payload. Used
// by save-step to know where to drop the parsed payload.
func PayloadKey(stepName string) string { return payloadKey(stepName) }

func payloadKey(name string) string {
	switch name {
	case "PRD":
		return "prd"
	case "TASKS_MANIFEST":
		return "tasksManifest"
	case "TASK":
		return "task"
	case "BRAINSTORMING":
		return "brainstorm"
	case "CODE_REVIEW":
		return "codeReview"
	case "RECEIVING_CODE_REVIEW":
		return "receivingCodeReview"
	case "WRITE_TESTS":
		return "writeTests"
	case "UPDATE_DOCS":
		return "updateDocs"
	case "FEATURE_ACCEPTANCE":
		return "featureAcceptance"
	case "COMMIT":
		return "commit"
	}
	return ""
}

// MustMarshal is a small helper used by tests / templates that need to embed
// a structured value as JSON. Errors panic (callers control input).
func MustMarshal(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(b)
}
