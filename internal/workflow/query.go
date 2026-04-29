package workflow

import (
	"fmt"
	"maps"
	"sort"
	"strings"
)

// QueryName is the canonical identifier for a pre-baked workflow query.
// Skills consume queries via `browzer workflow query <name>` instead of
// hand-writing schema-aware jq pipelines (WF-CLI-1, WF-MIG-1).
type QueryName = string

// Registered query names. Keep in sync with the entries in QueryRegistry.
const (
	QueryReusedGates              QueryName = "reused-gates"
	QueryFailedFindings           QueryName = "failed-findings"
	QueryOpenDeferredActions      QueryName = "open-deferred-actions"
	QueryTaskGatesBaseline        QueryName = "task-gates-baseline"
	QueryChangedFiles             QueryName = "changed-files"
	QueryDeferredScopeAdjustments QueryName = "deferred-scope-adjustments"
	QueryOpenFindings             QueryName = "open-findings"
	QueryNextStepID               QueryName = "next-step-id"
	// Cache pre-warm queries (added 2026-04-29, Phase 5 feature cache).
	// Note: explore queries are NOT pre-warmable — they are on-demand
	// (user intent varies per turn). Only deps + mentions cover the
	// deterministic blast-radius surfaces that benefit from pre-warming.
	QueryCacheWarmDeps     QueryName = "cache-warm-deps"
	QueryCacheWarmMentions QueryName = "cache-warm-mentions"
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
		QueryReusedGates: {
			Name:        QueryReusedGates,
			Description: "Gate keys that ran non-failingly across all completed TASK steps (gate-reuse audit for code-review).",
			Run:         queryReusedGates,
		},
		QueryFailedFindings: {
			Name:        QueryFailedFindings,
			Description: "Open code-review findings ordered by severity (high → medium → low) for fix-findings dispatch.",
			Run:         queryFailedFindings,
		},
		QueryOpenDeferredActions: {
			Name:        QueryOpenDeferredActions,
			Description: "FEATURE_ACCEPTANCE.operatorActionsRequested entries with resolved=false (orchestrator pause logic).",
			Run:         queryOpenDeferredActions,
		},
		QueryTaskGatesBaseline: {
			Name:        QueryTaskGatesBaseline,
			Description: "Aggregated baseline gates across completed TASK steps (per-gate verdict + last-source step).",
			Run:         queryTaskGatesBaseline,
		},
		QueryChangedFiles: {
			Name:        QueryChangedFiles,
			Description: "Union of files modified + created across all TASK and FIX_FINDINGS steps (deduped).",
			Run:         queryChangedFiles,
		},
		QueryDeferredScopeAdjustments: {
			Name:        QueryDeferredScopeAdjustments,
			Description: "Scope adjustments from prior TASK steps marked deferred / operator-followup (feature-acceptance §2.2).",
			Run:         queryDeferredScopeAdjustments,
		},
		QueryOpenFindings: {
			Name:        QueryOpenFindings,
			Description: "All CODE_REVIEW findings with status==open (orchestrator fix-findings loop).",
			Run:         queryOpenFindings,
		},
		QueryNextStepID: {
			Name:        QueryNextStepID,
			Description: "Next monotonic step ordinal (max(STEP_NN) + 1) — for skill stepId derivation.",
			Run:         queryNextStepID,
		},
		QueryCacheWarmDeps: {
			Name: QueryCacheWarmDeps,
			Description: "For each unique file in any TASK step's task.scope[], emit { file, depsCachePath } " +
				"pointing to <featDir>/.browzer-cache/deps/<slug>.json. The orchestrator (Step 2.5) iterates " +
				"this output and runs `browzer deps <file> --save <depsCachePath>` in parallel. " +
				"This query does NOT run browzer deps itself — callers own the execution.",
			Run: queryCacheWarmDeps,
		},
		QueryCacheWarmMentions: {
			Name: QueryCacheWarmMentions,
			Description: "For each unique file in any TASK step's task.scope[], emit { file, mentionsCachePath } " +
				"pointing to <featDir>/.browzer-cache/mentions/<slug>.json. The orchestrator (Step 2.5) iterates " +
				"this output and runs `browzer mentions <file> --save <mentionsCachePath>` in parallel. " +
				"This query does NOT run browzer mentions itself — callers own the execution.",
			Run: queryCacheWarmMentions,
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

// nestedSlice returns m[key] as []any, or nil.
func nestedSlice(m map[string]any, key string) []any {
	v, ok := m[key]
	if !ok {
		return nil
	}
	s, _ := v.([]any)
	return s
}

// ── reused-gates ──────────────────────────────────────────────────────────────

// queryReusedGates returns the deduped, sorted slice of gate keys whose value
// is non-null, non-empty, and not "fail" across every completed TASK step's
// `.task.execution.gates.postChange` map. Mirrors the audit jq from
// code-review/SKILL.md Phase 1.
func queryReusedGates(raw map[string]any) (any, error) {
	keys := make(map[string]struct{})
	for _, s := range stepsArray(raw) {
		step := stepObject(s)
		if step == nil {
			continue
		}
		if stringField(step, "name") != StepTask || stringField(step, "status") != StatusCompleted {
			continue
		}
		gates := nestedMap(nestedMap(nestedMap(step, "task"), "execution"), "gates")
		post := nestedMap(gates, "postChange")
		if post == nil {
			continue
		}
		for k, v := range post {
			if v == nil {
				continue
			}
			s, ok := v.(string)
			if !ok {
				// Non-string verdict (e.g. structured object) still counts as "ran without failing"
				// when distinguishable from "fail". Treat any non-string as reused.
				keys[k] = struct{}{}
				continue
			}
			if s == "" || s == "fail" {
				continue
			}
			keys[k] = struct{}{}
		}
	}
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// ── failed-findings ───────────────────────────────────────────────────────────

// severityRank orders findings high → medium → low, with anything else last.
func severityRank(sev string) int {
	switch strings.ToLower(sev) {
	case "high":
		return 0
	case "medium":
		return 1
	case "low":
		return 2
	default:
		return 3
	}
}

// queryFailedFindings returns CODE_REVIEW findings whose status is "open" and
// severity is high or medium, ordered by severity then findingId. Each entry
// is a map with id, severity, file, line, description, suggestedFix, status.
func queryFailedFindings(raw map[string]any) (any, error) {
	type entry struct {
		original map[string]any
		severity string
		id       string
	}
	var entries []entry
	for _, s := range stepsArray(raw) {
		step := stepObject(s)
		if step == nil {
			continue
		}
		if stringField(step, "name") != StepCodeReview {
			continue
		}
		findings := nestedSlice(nestedMap(step, "codeReview"), "findings")
		for _, f := range findings {
			fm, ok := f.(map[string]any)
			if !ok {
				continue
			}
			status := stringField(fm, "status")
			if status != "" && status != "open" {
				continue
			}
			sev := stringField(fm, "severity")
			r := severityRank(sev)
			if r > 1 {
				continue
			}
			entries = append(entries, entry{original: fm, severity: sev, id: stringField(fm, "id")})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		ri, rj := severityRank(entries[i].severity), severityRank(entries[j].severity)
		if ri != rj {
			return ri < rj
		}
		return entries[i].id < entries[j].id
	})
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.original)
	}
	return out, nil
}

// ── open-deferred-actions ─────────────────────────────────────────────────────

// queryOpenDeferredActions returns every FEATURE_ACCEPTANCE.operatorActionsRequested
// entry with resolved==false across ALL feature-acceptance steps (re-invocations
// included).
func queryOpenDeferredActions(raw map[string]any) (any, error) {
	out := make([]map[string]any, 0)
	for _, s := range stepsArray(raw) {
		step := stepObject(s)
		if step == nil {
			continue
		}
		if stringField(step, "name") != StepFeatureAcceptance {
			continue
		}
		actions := nestedSlice(nestedMap(step, "featureAcceptance"), "operatorActionsRequested")
		for _, a := range actions {
			am, ok := a.(map[string]any)
			if !ok {
				continue
			}
			if resolved, ok := am["resolved"].(bool); ok && resolved {
				continue
			}
			// Tag the originating step for the consumer's audit trail.
			tagged := make(map[string]any, len(am)+1)
			maps.Copy(tagged, am)
			tagged["sourceStepId"] = stringField(step, "stepId")
			out = append(out, tagged)
		}
	}
	return out, nil
}

// ── task-gates-baseline ───────────────────────────────────────────────────────

// queryTaskGatesBaseline returns a map keyed by gate name with the latest
// recorded verdict and the step it came from. Used by code-review Phase 1 to
// decide which gates can be skipped vs re-run.
func queryTaskGatesBaseline(raw map[string]any) (any, error) {
	out := make(map[string]map[string]any)
	for _, s := range stepsArray(raw) {
		step := stepObject(s)
		if step == nil {
			continue
		}
		if stringField(step, "name") != StepTask || stringField(step, "status") != StatusCompleted {
			continue
		}
		stepID := stringField(step, "stepId")
		post := nestedMap(nestedMap(nestedMap(nestedMap(step, "task"), "execution"), "gates"), "postChange")
		for k, v := range post {
			out[k] = map[string]any{
				"verdict":      v,
				"sourceStepId": stepID,
			}
		}
	}
	return out, nil
}

// ── changed-files ─────────────────────────────────────────────────────────────

// appendStrings appends raw[].(string) entries to dst.
func appendStrings(dst []string, raw []any) []string {
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			continue
		}
		dst = append(dst, s)
	}
	return dst
}

// queryChangedFiles returns the deduped, sorted union of modified + created
// files across every TASK step (.task.execution.files.{modified,created}),
// every RECEIVING_CODE_REVIEW dispatch (.receivingCodeReview.dispatches[].filesChanged),
// the WRITE_TESTS step's authored files (.writeTests.filesAuthored), and the
// legacy FIX_FINDINGS dispatch (.fixFindings.dispatches[].filesChanged) for
// backwards compat with pre-redesign workflow.json files.
func queryChangedFiles(raw map[string]any) (any, error) {
	seen := make(map[string]struct{})
	var collected []string
	for _, s := range stepsArray(raw) {
		step := stepObject(s)
		if step == nil {
			continue
		}
		switch stringField(step, "name") {
		case StepTask:
			files := nestedMap(nestedMap(step, "task"), "execution")
			files = nestedMap(files, "files")
			collected = appendStrings(collected, nestedSlice(files, "modified"))
			collected = appendStrings(collected, nestedSlice(files, "created"))
		case StepReceivingCodeReview:
			dispatches := nestedSlice(nestedMap(step, "receivingCodeReview"), "dispatches")
			for _, d := range dispatches {
				dm, ok := d.(map[string]any)
				if !ok {
					continue
				}
				collected = appendStrings(collected, nestedSlice(dm, "filesChanged"))
			}
		case StepWriteTests:
			collected = appendStrings(collected, nestedSlice(nestedMap(step, "writeTests"), "filesAuthored"))
		case StepFixFindings:
			// Legacy: pre-redesign workflow.json files keyed dispatches under fixFindings.
			dispatches := nestedSlice(nestedMap(step, "fixFindings"), "dispatches")
			for _, d := range dispatches {
				dm, ok := d.(map[string]any)
				if !ok {
					continue
				}
				collected = appendStrings(collected, nestedSlice(dm, "filesChanged"))
			}
		}
	}
	out := make([]string, 0, len(collected))
	for _, f := range collected {
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	sort.Strings(out)
	return out, nil
}

// ── deferred-scope-adjustments ────────────────────────────────────────────────

// queryDeferredScopeAdjustments walks every step's
// .task.execution.scopeAdjustments[] and returns entries whose owner, reason,
// or adjustment text contains a deferred-marker keyword. Mirrors the
// feature-acceptance §2.2 audit pipeline.
func queryDeferredScopeAdjustments(raw map[string]any) (any, error) {
	deferredOwner := []string{"operator", "deferred", "follow-up", "followup"}
	deferredReason := []string{"deferred", "operator", "environment", "live", "staging", "deploy-time", "deploy time"}
	deferredAdjustment := []string{"deferred", "skipped", "operator-followup"}

	matches := func(text string, needles []string) bool {
		if text == "" {
			return false
		}
		lc := strings.ToLower(text)
		for _, n := range needles {
			if strings.Contains(lc, n) {
				return true
			}
		}
		return false
	}

	type adjKey struct {
		adjustment string
		reason     string
	}
	seen := make(map[adjKey]struct{})
	out := make([]map[string]any, 0)

	for _, s := range stepsArray(raw) {
		step := stepObject(s)
		if step == nil {
			continue
		}
		stepID := stringField(step, "stepId")
		adjs := nestedSlice(nestedMap(nestedMap(step, "task"), "execution"), "scopeAdjustments")
		for _, a := range adjs {
			am, ok := a.(map[string]any)
			if !ok {
				continue
			}
			owner := stringField(am, "owner")
			reason := stringField(am, "reason")
			adjustment := stringField(am, "adjustment")
			resolution := stringField(am, "resolution")
			if !matches(owner, deferredOwner) && !matches(reason, deferredReason) && !matches(adjustment, deferredAdjustment) {
				continue
			}
			k := adjKey{adjustment: adjustment, reason: reason}
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, map[string]any{
				"stepId":     stepID,
				"adjustment": adjustment,
				"reason":     reason,
				"resolution": resolution,
				"owner":      owner,
			})
		}
	}
	return out, nil
}

// ── open-findings ─────────────────────────────────────────────────────────────

// queryOpenFindings returns every CODE_REVIEW finding with status=="open"
// regardless of severity. Used by orchestrate-task-delivery §3.5 to drive the
// fix-findings loop.
func queryOpenFindings(raw map[string]any) (any, error) {
	out := make([]map[string]any, 0)
	for _, s := range stepsArray(raw) {
		step := stepObject(s)
		if step == nil {
			continue
		}
		if stringField(step, "name") != StepCodeReview {
			continue
		}
		stepID := stringField(step, "stepId")
		findings := nestedSlice(nestedMap(step, "codeReview"), "findings")
		for _, f := range findings {
			fm, ok := f.(map[string]any)
			if !ok {
				continue
			}
			status := stringField(fm, "status")
			if status != "" && status != "open" {
				continue
			}
			tagged := make(map[string]any, len(fm)+1)
			maps.Copy(tagged, fm)
			tagged["sourceStepId"] = stepID
			out = append(out, tagged)
		}
	}
	return out, nil
}

// ── cache-warm helpers ────────────────────────────────────────────────────────

// fileSlug converts a file path to a safe filename component by replacing
// path separators and other non-identifier characters with underscores.
// Mirrors the `tr / _` convention used by existing cr-deps-<slug>.json
// patterns in the code-review skill.
//
// Cache layout (documented here; runtime dirs are created by the orchestrator):
//
//	$WORKFLOW_DIR/.browzer-cache/
//	  deps/<file-slug>.json       # output of `browzer deps <path> --save`
//	  rdeps/<file-slug>.json      # output of `browzer deps <path> --reverse --save`
//	  mentions/<file-slug>.json   # output of `browzer mentions <path> --save`
func fileSlug(path string) string {
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		" ", "_",
		".", "_",
	)
	return replacer.Replace(path)
}

// collectTaskScopeFiles returns the deduped, sorted union of all file paths
// listed in task.scope[] across every TASK step. It reads the top-level
// .featDir to construct cache paths. When featDir is absent or empty the
// paths are relative to ".browzer-cache" (callers should handle this).
func collectTaskScopeFiles(raw map[string]any) (files []string, featDir string) {
	if d, ok := raw["featDir"].(string); ok {
		featDir = d
	}
	seen := make(map[string]struct{})
	for _, s := range stepsArray(raw) {
		step := stepObject(s)
		if step == nil || stringField(step, "name") != StepTask {
			continue
		}
		// task.scope is []string in the schema (file paths for the task).
		taskPayload := nestedMap(step, "task")
		scope := nestedSlice(taskPayload, "scope")
		for _, v := range scope {
			f, ok := v.(string)
			if !ok || f == "" {
				continue
			}
			if _, dup := seen[f]; dup {
				continue
			}
			seen[f] = struct{}{}
			files = append(files, f)
		}
	}
	sort.Strings(files)
	return files, featDir
}

// ── next-step-id ──────────────────────────────────────────────────────────────

// queryNextStepID returns the next monotonic step ordinal as an integer (max
// observed STEP_NN ordinal + 1, or 1 if none). The caller is responsible for
// formatting the ID as `STEP_<NN>_<NAME>` — this query only returns the integer
// to keep the contract simple.
func queryNextStepID(raw map[string]any) (any, error) {
	max := 0
	for _, s := range stepsArray(raw) {
		step := stepObject(s)
		if step == nil {
			continue
		}
		id := stringField(step, "stepId")
		if !strings.HasPrefix(id, "STEP_") {
			continue
		}
		// Schema-mandated step ID shape is STEP_<digits>_<name> — anything
		// without the trailing `_<name>` (no second underscore) is not a
		// well-formed ordinal and is silently skipped, same as a non-numeric
		// prefix. We could lift this to tolerate `STEP_05` as a valid bare
		// ordinal, but the schema forbids that shape so accepting it would
		// hide drift. Keep parsing strict.
		rest := strings.TrimPrefix(id, "STEP_")
		idx := strings.IndexByte(rest, '_')
		if idx <= 0 {
			continue
		}
		n := 0
		if _, err := fmt.Sscanf(rest[:idx], "%d", &n); err != nil {
			continue
		}
		if n > max {
			max = n
		}
	}
	return max + 1, nil
}

// ── cache-warm-deps ───────────────────────────────────────────────────────────

// CacheWarmRecord is the JSON-serialisable output record for cache-warm-* queries.
type CacheWarmRecord struct {
	File      string `json:"file"`
	CachePath string `json:"depsCachePath,omitempty"`
	// MentionsCachePath is populated for the cache-warm-mentions query.
	MentionsCachePath string `json:"mentionsCachePath,omitempty"`
}

// queryCacheWarmDeps iterates over all TASK steps' task.scope[] (deduped union)
// and for each file emits a CacheWarmRecord with the file path and the path
// where the orchestrator should write the deps cache JSON.
//
// Output shape: []{ file, depsCachePath }
//
// The orchestrator (Step 2.5) iterates this output and runs:
//
//	browzer deps "$FILE" --json --save "$CACHE_PATH" &
//	browzer deps "$FILE" --reverse --json --save "${CACHE_PATH/deps/rdeps}" &
//
// This query does NOT run browzer deps — callers own the execution. Keeping
// the CLI orthogonal avoids coupling the query layer to external tools.
func queryCacheWarmDeps(raw map[string]any) (any, error) {
	files, featDir := collectTaskScopeFiles(raw)
	cacheBase := featDir + "/.browzer-cache/deps"
	out := make([]CacheWarmRecord, 0, len(files))
	for _, f := range files {
		out = append(out, CacheWarmRecord{
			File:      f,
			CachePath: cacheBase + "/" + fileSlug(f) + ".json",
		})
	}
	return out, nil
}

// ── cache-warm-mentions ───────────────────────────────────────────────────────

// queryCacheWarmMentions iterates over all TASK steps' task.scope[] (deduped
// union) and for each file emits a CacheWarmRecord with the file path and the
// path where the orchestrator should write the mentions cache JSON.
//
// Output shape: []{ file, mentionsCachePath }
//
// The orchestrator (Step 2.5) iterates this output and runs:
//
//	browzer mentions "$FILE" --json --save "$CACHE_PATH" &
//
// This query does NOT run browzer mentions — callers own the execution.
func queryCacheWarmMentions(raw map[string]any) (any, error) {
	files, featDir := collectTaskScopeFiles(raw)
	cacheBase := featDir + "/.browzer-cache/mentions"
	out := make([]CacheWarmRecord, 0, len(files))
	for _, f := range files {
		out = append(out, CacheWarmRecord{
			File:              f,
			MentionsCachePath: cacheBase + "/" + fileSlug(f) + ".json",
		})
	}
	return out, nil
}
