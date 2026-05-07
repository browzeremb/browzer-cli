package workflow

// QueryName is the canonical identifier for a pre-baked workflow query.
// Skills consume queries via `browzer workflow query <name>` instead of
// hand-writing schema-aware jq pipelines (WF-CLI-1, WF-MIG-1).
type QueryName = string

// Registered query names. Keep in sync with the entries in QueryRegistry.
const (
	QueryTasksManifest QueryName = "tasks-manifest"
	QueryStepsByName   QueryName = "steps-by-name"
	QueryStepsByOwner  QueryName = "steps-by-owner"
)

// QueryDefinition describes a single registered query.
type QueryDefinition struct {
	Name        QueryName
	Description string
	Run         func(raw map[string]any) (any, error)
}

// QueryRegistry returns the canonical map of registered queries. Each query is
// implemented in Go (no jq), validated against the v1 schema shape, and emits
// a JSON-serialisable result. Adding a query: append to this map and add a
// test case in query_test.go.
func QueryRegistry() map[QueryName]QueryDefinition {
	return map[QueryName]QueryDefinition{
		QueryTasksManifest: {
			Name: QueryTasksManifest,
			Description: "Payload of the (single) TASKS_MANIFEST step's tasksManifest field — " +
				"orchestrator + execute-with-teams iterate tasksOrder/parallelizable. " +
				"Returns null when no TASKS_MANIFEST step is present.",
			Run: queryTasksManifest,
		},
		QueryStepsByName: {
			Name: QueryStepsByName,
			Description: "Steps grouped by their name field — emits {<StepName>: [<step>, …], …} " +
				"in document order. Skills index `[0]` for first / `[-1]` for last to derive " +
				"step references (e.g. last CODE_REVIEW, first BRAINSTORMING).",
			Run: queryStepsByName,
		},
		QueryStepsByOwner: {
			Name: QueryStepsByOwner,
			Description: "Steps grouped by their owner field — emits {<owner>: [<step>, …], …} " +
				"in document order. Worktree rendezvous reads its own workflow.json copy and " +
				"selects only the steps it owns before patching them back into the main workflow.",
			Run: queryStepsByOwner,
		},
	}
}

// stepsArray returns the raw `.steps[]` slice or nil if absent.
func stepsArray(raw map[string]any) []any {
	stepsRaw, ok := raw["steps"]
	if !ok {
		return nil
	}
	steps, ok := stepsRaw.([]any)
	if !ok {
		return nil
	}
	return steps
}

// stepObject returns the step at index i as a map, or nil if not a map.
func stepObject(s any) map[string]any {
	m, _ := s.(map[string]any)
	return m
}

// stringField returns the string value at key, or "" if absent / not a string.
func stringField(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// nestedMap returns m[key] as a map[string]any, or nil if absent / wrong type.
func nestedMap(m map[string]any, key string) map[string]any {
	v, ok := m[key]
	if !ok {
		return nil
	}
	mm, _ := v.(map[string]any)
	return mm
}

// ── steps-by-name ─────────────────────────────────────────────────────────────

// queryStepsByName groups steps by their `name` field in document order. The
// returned map is JSON-serialisable as `{<StepName>: [<step>, …], …}`. Callers
// pick first / last with jq `[0]` / `[-1]` indexing.
func queryStepsByName(raw map[string]any) (any, error) {
	out := map[string][]any{}
	for _, s := range stepsArray(raw) {
		step := stepObject(s)
		if step == nil {
			continue
		}
		name := stringField(step, "name")
		if name == "" {
			continue
		}
		out[name] = append(out[name], step)
	}
	return out, nil
}

// ── steps-by-owner ────────────────────────────────────────────────────────────

// queryStepsByOwner groups steps by their `owner` field in document order.
// Returned shape: `{<owner>: [<step>, …], …}`. Steps without an `owner` field
// are silently skipped (rendezvous only cares about owned steps). The empty-
// string owner is also skipped to avoid bucketing un-claimed steps.
func queryStepsByOwner(raw map[string]any) (any, error) {
	out := map[string][]any{}
	for _, s := range stepsArray(raw) {
		step := stepObject(s)
		if step == nil {
			continue
		}
		owner := stringField(step, "owner")
		if owner == "" {
			continue
		}
		out[owner] = append(out[owner], step)
	}
	return out, nil
}

// ── tasks-manifest ────────────────────────────────────────────────────────────

// queryTasksManifest returns the `tasksManifest` payload of the (single)
// TASKS_MANIFEST step. The orchestrator and execute-with-teams iterate
// `.tasksOrder[]` and `.parallelizable` from this payload. Returns nil when no
// TASKS_MANIFEST step is present so callers can detect absence with `null`.
func queryTasksManifest(raw map[string]any) (any, error) {
	for _, s := range stepsArray(raw) {
		step := stepObject(s)
		if step == nil {
			continue
		}
		if stringField(step, "name") != "TASKS_MANIFEST" {
			continue
		}
		return nestedMap(step, "tasksManifest"), nil
	}
	return nil, nil
}
