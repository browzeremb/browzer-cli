// Package schema — validator.go
//
// CUE-based workflow.json validator. Single source of truth: the embedded
// `packages/cli/schemas/workflow-v1.cue` SSOT (TASK_01). Both the standalone
// CLI write path (`internal/workflow/apply.go`) and the daemon goroutine
// path (`internal/daemon/methods.go`) call ValidateWorkflow with the
// post-mutation JSON before any tmp+rename commit so a failed validation
// leaves the file bit-identical on disk.
//
// Public API:
//
//   ValidateWorkflow(rawJSON []byte) ValidationResult
//   ValidateStep(stepJSON []byte, stepType string) ValidationResult
//
// Both return a `ValidationResult` with a deterministic `Violations[]`
// (sorted by Path, then Code, then Message) so two runs against the same
// payload produce byte-identical output. Stable identifiers in `Code`
// allow downstream skills to grep / map onto remediation actions.
//
// Performance: the CUE schema is compiled exactly once per process via
// sync.Once; subsequent calls reuse the cached cue.Value. NFR-2 budget is
// ≤30 ms per validation (excluding tmp+rename); BenchmarkValidate enforces
// it.
//
// Bypass: callers may opt out via `args.NoSchemaCheck=true` (CLI:
// `--no-schema-check`). The bypass MUST be paired with an audit-log entry
// in `<repo-root>/.browzer/audit/no-schema-check.log` (timestamp + sha256
// of the rejected payload). The audit-log writer lives in this package
// (RecordNoSchemaCheck) so callers don't fan-out the write paths.
package schema

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cueerrors "cuelang.org/go/cue/errors"
	cuejson "cuelang.org/go/encoding/json"
)

// embeddedCueSchema is the CUE SSOT mirrored from
// `packages/cli/schemas/workflow-v1.cue` into this package directory so
// `go:embed` (which forbids `..` traversal) can pull it into the binary.
// The Makefile under packages/cli/schemas/ keeps the two copies in sync.
//
//go:embed workflow-v1.cue
var embeddedCueSchema string

// defaultAddedIn is the @addedIn default applied when a violation cannot
// be traced to a specific schema field (e.g. a structural type-mismatch
// at the document root). Matches the schema's pre-existing fields cutoff
// from `workflow-v1.cue`.
const defaultAddedIn = "2026-04-24T00:00:00Z"

// schemaPath is the CUE file path embedded into the binary. Only used as
// a label in CUE error reporting; never opened at runtime.
const schemaPath = "schemas/workflow-v1.cue"

// ValidationResult is the return shape of ValidateWorkflow / ValidateStep.
//
// Valid is true iff Violations is empty AFTER deterministic sorting.
// Callers MUST treat the slice as read-only — copying is cheap (small N
// in practice) and avoids accidental ordering bugs in pipelines.
type ValidationResult struct {
	Valid      bool        `json:"valid"`
	Violations []Violation `json:"violations"`
}

// Violation is one structural / type / pattern / enum / cardinality
// failure surfaced by the CUE engine.
//
// Path is a JSON Pointer-like dotted string (e.g.
// `steps[0].task.execution.scopeAdjustments[0].kind`) so downstream
// skills can grep / map onto specific schema fields.
//
// Code is one of a small stable set: missing-required-field,
// invalid-enum-value, invalid-pattern, type-mismatch, structural-error,
// unknown-error. Adding a new code is a SemVer-minor change for the
// downstream skill rubric.
//
// AddedIn is the ISO-8601 timestamp from the corresponding `@addedIn(...)`
// CUE attribute. Defaults to `defaultAddedIn` when no attribute matches
// (e.g. structural mismatches that don't bind to a single field). Surfacing
// AddedIn lets the judge skill ignore violations newer than the workflow's
// `startedAt` (backwards-compat window — see the rubric rewrite preview in
// docs/WORKFLOW_SYNC_REDESIGN.md Appendix A).
//
// Field is the trailing identifier of Path (last segment after `.`/`]`)
// for cheap UI rendering. Empty when Path is empty (root-level error).
//
// AllowedFields is populated when Code == "unknown-field": the closed-struct
// parent schema's accepted field labels in alphabetical order. Lets a caller
// render `unknown field "foo" — allowed: bar, baz`.
//
// AllowedValues is populated when Code == "invalid-enum-value": the literal
// CUE-disjunction members the field accepts, alphabetically sorted. Lets a
// caller render `invalid value "deferred" — allowed: pass, fail, skip`.
//
// Both slices are nil (omitted from JSON) when the engine could not resolve
// the parent / disjunction definition — keeps the JSON shape stable for the
// historic `unknown-field` / `invalid-enum-value` codes that did not carry
// these hints.
type Violation struct {
	Path          string   `json:"path"`
	Code          string   `json:"code"`
	Message       string   `json:"message"`
	AddedIn       string   `json:"addedIn"`
	Field         string   `json:"field"`
	AllowedFields []string `json:"allowedFields,omitempty"`
	AllowedValues []string `json:"allowedValues,omitempty"`
}

// internal — CUE compilation + @addedIn lookup table cached once per process.
//
// F-02 (2026-05-04): the CUE context is cached alongside the compiled
// cue.Value. Earlier code allocated a fresh `cuecontext.New()` in
// ValidateWorkflow per call, which left the cached schema value bound to a
// different runtime than the payload — `Unify` between cross-runtime values
// produces a bottom error and silently broke validation. Reusing the cached
// context fixes the cross-runtime contamination AND retires the per-call
// runtime allocation.
//
// F-03 (2026-05-04): the per-definition cue.Value cache (`defs`) lets
// describe.go's DescribeStepType look up `#PRD`, `#TaskExecution`, etc.
// without re-compiling the whole SSOT on every call. Lookups are guarded
// by `defsMu` (sync.Mutex) so concurrent describe calls populate the map
// safely. The first call per definition runs the LookupPath; subsequent
// calls hit the map in O(1).
type schemaCache struct {
	once    sync.Once
	ctx     *cue.Context
	value   cue.Value     // resolved root: #WorkflowV1
	root    cue.Value     // raw compiled SSOT (for definition lookups)
	addedIn map[string]string // dotted-path → ISO timestamp
	err     error

	defsMu sync.Mutex
	defs   map[string]cue.Value
}

var schemaSingleton schemaCache

// loadSchema returns the cached cue.Context + cue.Value + per-field
// @addedIn map. First call compiles the embedded SSOT; subsequent calls
// are O(1).
func loadSchema() (*cue.Context, cue.Value, map[string]string, error) {
	schemaSingleton.once.Do(func() {
		ctx := cuecontext.New()
		v := ctx.CompileString(embeddedCueSchema, cue.Filename(schemaPath))
		if err := v.Err(); err != nil {
			schemaSingleton.err = fmt.Errorf("schema: compile embedded cue: %w", err)
			return
		}
		root := v.LookupPath(cue.ParsePath("#WorkflowV1"))
		if !root.Exists() {
			schemaSingleton.err = stderrors.New("schema: #WorkflowV1 definition missing from embedded cue")
			return
		}
		schemaSingleton.ctx = ctx
		schemaSingleton.value = root
		schemaSingleton.root = v
		schemaSingleton.addedIn = parseAddedInMap(embeddedCueSchema)
		schemaSingleton.defs = map[string]cue.Value{
			"#WorkflowV1": root,
		}
	})
	return schemaSingleton.ctx, schemaSingleton.value, schemaSingleton.addedIn, schemaSingleton.err
}

// lookupDefinition returns the cue.Value for `defName` (e.g. `#PRD`,
// `#TaskExecution`). Cached after the first call per definition. Returns
// (cue.Value{}, false) when the definition is missing from the SSOT —
// the caller is expected to surface a "definition not found" error so
// the failure remains observable.
//
// F-03 (2026-05-04): replaces describe.go's per-call CompileString +
// LookupPath, which paid the ~50ms compile cost on every invocation.
func lookupDefinition(defName string) (cue.Value, bool) {
	schemaSingleton.defsMu.Lock()
	defer schemaSingleton.defsMu.Unlock()
	if v, ok := schemaSingleton.defs[defName]; ok {
		return v, true
	}
	if !schemaSingleton.root.Exists() {
		return cue.Value{}, false
	}
	v := schemaSingleton.root.LookupPath(cue.ParsePath(defName))
	if !v.Exists() {
		return cue.Value{}, false
	}
	schemaSingleton.defs[defName] = v
	return v, true
}

// ValidateWorkflow validates a workflow.json document against the
// embedded #WorkflowV1 definition. Empty input returns one
// `unknown-error` violation rather than panicking.
//
// Determinism: violations are sorted by (Path, Code, Message) and the
// returned slice is byte-stable across runs.
func ValidateWorkflow(rawJSON []byte) ValidationResult {
	if len(rawJSON) == 0 {
		return ValidationResult{
			Valid: false,
			Violations: []Violation{{
				Path:    "",
				Code:    "unknown-error",
				Message: "empty workflow payload",
				AddedIn: defaultAddedIn,
			}},
		}
	}
	ctx, schemaValue, addedIn, err := loadSchema()
	if err != nil {
		return ValidationResult{
			Valid: false,
			Violations: []Violation{{
				Path:    "",
				Code:    "unknown-error",
				Message: err.Error(),
				AddedIn: defaultAddedIn,
			}},
		}
	}

	// F-02 (2026-05-04): reuse the cached cue.Context. Previously this
	// site allocated a fresh `cuecontext.New()`, which broke `Unify` —
	// CUE values from different runtimes can't be unified and the call
	// produced a structural-error bottom rather than the real violation.

	// Decode JSON into CUE via the cue/encoding/json adapter — this
	// preserves JSON numeric types (int vs float) better than the
	// generic ctx.Encode(any) path which routes everything through
	// float64 (the encoding/json default for `any`).
	expr, jsonErr := cuejson.Extract("payload.json", rawJSON)
	if jsonErr != nil {
		return ValidationResult{
			Valid: false,
			Violations: []Violation{{
				Path:    "",
				Code:    "structural-error",
				Message: "workflow payload is not valid JSON: " + jsonErr.Error(),
				AddedIn: defaultAddedIn,
			}},
		}
	}
	payload := ctx.BuildExpr(expr)
	if pErr := payload.Err(); pErr != nil {
		return ValidationResult{
			Valid: false,
			Violations: []Violation{{
				Path:    "",
				Code:    "structural-error",
				Message: "encode payload as cue: " + pErr.Error(),
				AddedIn: defaultAddedIn,
			}},
		}
	}

	unified := schemaValue.Unify(payload)
	vErr := unified.Validate(cue.Concrete(true), cue.All())
	violations := convertCueErrors(vErr, addedIn)

	// WF-CUE-NOISE-02 enrichment: when the disjunction narrowing failed
	// for a steps[N] element, CUE compresses the inner branch errors
	// into a placeholder counter that never surfaces the leaf
	// constraint failure (e.g. `lint = "pending"`). We re-validate
	// every failing step against its declared `name`-specific
	// definition (`#TaskStep` etc) and append those leaf errors so
	// FormatViolations can render them.
	if vErr != nil {
		violations = append(violations, repairStepLeafViolations(rawJSON, ctx, addedIn, violations)...)
	}

	// A1.5 (2026-05-05): enrich Violations with allowedFields /
	// allowedValues so an LLM caller never has to grep the SSOT to see
	// the legal set. The enricher mutates Code from `type-mismatch` to
	// `invalid-enum-value` when the path resolves to a string-disjunction
	// field — which is exactly the friction case `lint: "deferred …"`
	// → `conflicting values "skip" and "deferred …"`. Best-effort: any
	// schema-walk failure leaves the violation untouched.
	//
	// Runs AFTER repairStepLeafViolations so the appended per-step-type
	// re-validation rows (which carry richer paths than the workflow-root
	// disjunction-noise lines) are enriched too.
	enrichViolations(violations, schemaValue)

	sortViolations(violations)
	violations = dedupeViolations(violations)
	if violations == nil {
		violations = []Violation{}
	}
	return ValidationResult{Valid: len(violations) == 0, Violations: violations}
}

// dedupeViolations removes consecutive entries with identical
// (Path, Code, Message). The slice is assumed to be sorted by
// sortViolations beforehand. Used after WF-CUE-NOISE-02 enrichment
// where the leaf-recovery pass may produce a duplicate of an entry
// the original CUE tree already surfaced.
func dedupeViolations(vs []Violation) []Violation {
	if len(vs) < 2 {
		return vs
	}
	out := vs[:1]
	for _, v := range vs[1:] {
		last := out[len(out)-1]
		if v.Path == last.Path && v.Code == last.Code && v.Message == last.Message {
			continue
		}
		out = append(out, v)
	}
	return out
}

// stepNameToDefinition maps the canonical step name (`TASK`, `PRD`,
// `BRAINSTORMING`, …) to the CUE definition that holds its full
// per-step shape. Mirrors `#StepDefinitions` in workflow-v1.cue —
// kept as a Go map so changes to that block surface a compile error
// here rather than a silent miss.
var stepNameToDefinition = map[string]string{
	"PRD":                   "#PRDStep",
	"TASKS_MANIFEST":        "#TasksManifestStep",
	"TASK":                  "#TaskStep",
	"BRAINSTORMING":         "#BrainstormingStep",
	"CODE_REVIEW":           "#CodeReviewStep",
	"RECEIVING_CODE_REVIEW": "#ReceivingCodeReviewStep",
	"WRITE_TESTS":           "#WriteTestsStep",
	"UPDATE_DOCS":           "#UpdateDocsStep",
	"FEATURE_ACCEPTANCE":    "#FeatureAcceptanceStep",
	"COMMIT":                "#CommitStep",
}

// repairStepLeafViolations is the WF-CUE-NOISE-02 escape hatch.
//
// When the top-level `#WorkflowV1` validation surfaces only a
// disjunction-narrowing failure for a step (the noisy
// "conflicting values …" lines + empty-disjunction placeholders),
// the actual constraint failure that triggered the bottom value is
// hidden behind CUE's collapsing of the disjunction error tree.
// We recover it by re-validating the step against its declared
// step-type definition (selected by `name`), which produces a
// clean leaf-error tree (no narrowing).
//
// The function is best-effort:
//
//   - Steps without a recognisable `name` are skipped.
//   - When the per-step re-validation succeeds (the operator's
//     payload IS structurally consistent with its declared step
//     type), no violations are appended — the original disjunction
//     errors stand.
//   - Output paths are remapped to the steps[N].… form so they
//     line up with the rest of the diagnostic output.
//
// Determinism: violations are returned in arrival order; the caller
// (ValidateWorkflow) re-sorts the union via sortViolations.
func repairStepLeafViolations(rawJSON []byte, ctx *cue.Context, addedIn map[string]string, existing []Violation) []Violation {
	// Cheap check: only do enrichment work when the existing
	// violations include at least one steps[N] disjunction
	// placeholder OR sibling-narrowing entry. Otherwise the
	// schema engine already gave us actionable output.
	if !hasStepDisjunctionNoise(existing) {
		return nil
	}
	var doc struct {
		Steps []json.RawMessage `json:"steps"`
	}
	if jerr := json.Unmarshal(rawJSON, &doc); jerr != nil {
		return nil
	}
	var out []Violation
	for idx, raw := range doc.Steps {
		name := extractStepName(raw)
		if name == "" {
			continue
		}
		defName, ok := stepNameToDefinition[name]
		if !ok {
			continue
		}
		defVal, ok := lookupDefinition(defName)
		if !ok {
			continue
		}
		expr, jerr := cuejson.Extract("step.json", raw)
		if jerr != nil {
			continue
		}
		stepVal := ctx.BuildExpr(expr)
		if stepVal.Err() != nil {
			continue
		}
		unified := defVal.Unify(stepVal)
		vErr := unified.Validate(cue.Concrete(true), cue.All())
		if vErr == nil {
			continue
		}
		leaves := flattenCueErrors(vErr)
		prefix := fmt.Sprintf("#WorkflowV1.steps[%d]", idx)
		for _, ce := range leaves {
			path := joinCuePath(ce.Path())
			path = remapStepDefPath(path, defName, prefix)
			msg := errMessage(ce)
			// Drop leaf placeholders; we only want actionable
			// constraint failures here.
			if emptyDisjunctionRe.MatchString(strings.TrimPrefix(msg, defName+": ")) {
				continue
			}
			// Strip the redundant "#TaskStep: " / "#TaskStep.task…: "
			// prefix from the message — the path column already
			// carries it.
			cleanMsg := stripDefPrefix(msg, defName)
			out = append(out, Violation{
				Path:    path,
				Code:    classifyCode(cleanMsg),
				Message: cleanMsg,
				AddedIn: lookupAddedIn(addedIn, path),
				Field:   trailingField(path),
			})
		}
	}
	return out
}

// hasStepDisjunctionNoise reports whether the existing violation
// slice contains at least one entry that signals the disjunction
// narrowing path (placeholder counter or sibling-narrowing
// type-mismatch on a steps[N] element).
func hasStepDisjunctionNoise(vs []Violation) bool {
	for _, v := range vs {
		if !strings.Contains(v.Path, "steps[") && !strings.Contains(v.Path, "steps.") {
			continue
		}
		if emptyDisjunctionRe.MatchString(v.Message) {
			return true
		}
		if v.Code == "type-mismatch" && siblingNarrowingRe.MatchString(v.Message) {
			return true
		}
	}
	return false
}

// extractStepName pulls `name` out of a step JSON blob without a
// full unmarshal. Returns "" when the field is missing or not a
// string.
func extractStepName(raw json.RawMessage) string {
	var probe struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return ""
	}
	return probe.Name
}

// remapStepDefPath rewrites a CUE path like
// `#TaskStep.task.execution.gates.postChange.lint` to
// `steps[3].task.execution.gates.postChange.lint` so it composes
// with the rest of the workflow-level diagnostics.
func remapStepDefPath(path, defName, prefix string) string {
	if path == "" {
		return prefix
	}
	if strings.HasPrefix(path, defName+".") {
		return prefix + "." + strings.TrimPrefix(path, defName+".")
	}
	if path == defName {
		return prefix
	}
	return prefix + "." + path
}

// stripDefPrefix removes `<defName>:` and `<defName>.<...>:` lead-in
// from a CUE error message. The CUE engine prefixes each message
// with the path it was raised at; we already render the path
// independently in FormatViolations.
func stripDefPrefix(msg, defName string) string {
	if msg == "" {
		return msg
	}
	colonIdx := strings.Index(msg, ": ")
	if colonIdx < 0 {
		return msg
	}
	prefix := msg[:colonIdx]
	if prefix == defName || strings.HasPrefix(prefix, defName+".") {
		return strings.TrimSpace(msg[colonIdx+1:])
	}
	return msg
}

// ValidateStep validates a single step subtree against the matching
// step-type definition. stepType is the canonical step name
// (`PRD`, `TASK`, `BRAINSTORMING`, `CODE_REVIEW`, ...). When stepType is
// empty or unknown, the step is validated against #StepBase only (loosest
// shape) so the caller still sees structural errors.
//
// Today this is a thin wrapper that wraps the step in a synthetic
// `{steps: [<step>]}` document and delegates to ValidateWorkflow — keeps
// the @addedIn map + violation Path resolution consistent. Step-type
// dispatch is best-effort; a future revision may target a per-step CUE
// definition for cleaner error paths.
func ValidateStep(stepJSON []byte, stepType string) ValidationResult {
	if len(stepJSON) == 0 {
		return ValidationResult{
			Valid: false,
			Violations: []Violation{{
				Path:    "",
				Code:    "unknown-error",
				Message: "empty step payload",
				AddedIn: defaultAddedIn,
			}},
		}
	}
	var step map[string]any
	if jsonErr := json.Unmarshal(stepJSON, &step); jsonErr != nil {
		return ValidationResult{
			Valid: false,
			Violations: []Violation{{
				Path:    "",
				Code:    "structural-error",
				Message: "step payload is not valid JSON: " + jsonErr.Error(),
				AddedIn: defaultAddedIn,
			}},
		}
	}
	// Fill in canonical step name when caller passes stepType.
	if stepType != "" && step["name"] == nil {
		step["name"] = stepType
	}
	// Synthesise a minimal envelope so the step is reachable from
	// #WorkflowV1.steps[0]. Fields outside step[] are pinned to schema
	// defaults; if the schema later requires top-level fields without
	// defaults the wrapping fixture must be updated.
	envelope := map[string]any{
		"schemaVersion":   2,
		"pluginVersion":   nil,
		"featureId":       "feat-19700101-step-validate-fixture",
		"featureName":     "step-validate-fixture",
		"featDir":         "step-validate-fixture",
		"originalRequest": "",
		"operator":        map[string]any{"locale": ""},
		"config": map[string]any{
			"mode":  "autonomous",
			"setAt": "1970-01-01T00:00:00Z",
		},
		"startedAt":       "1970-01-01T00:00:00Z",
		"updatedAt":       "1970-01-01T00:00:00Z",
		"completedAt":     nil,
		"totalElapsedMin": 0,
		"currentStepId":   "",
		"nextStepId":      "",
		"totalSteps":      1,
		"completedSteps":  0,
		"notes":           []any{},
		"globalWarnings":  []any{},
		"steps":           []any{step},
	}
	wrapped, _ := json.Marshal(envelope)
	res := ValidateWorkflow(wrapped)
	// Strip the synthetic envelope from each Path so step callers see
	// `task.execution.scopeAdjustments[0].kind` instead of
	// `steps[0].task.execution...`.
	for i, v := range res.Violations {
		res.Violations[i].Path = strings.TrimPrefix(v.Path, "steps[0].")
		if res.Violations[i].Path == "steps[0]" {
			res.Violations[i].Path = ""
		}
	}
	sortViolations(res.Violations)
	return res
}

// FormatViolations renders one violation per line in the canonical
// stable-order format used by the CLI: `<path>: <code> at @addedIn(<iso>): <message>`.
// Empty input returns the empty string. Used by apply.go to construct the
// error message returned by ApplyAndPersist when the validator rejects a
// post-mutation payload.
//
// WF-CUE-NOISE-01 / WF-CUE-NOISE-02 (2026-05-04): the raw CUE engine
// surfaces two classes of noise that drown out the real constraint
// failure for operators staring at the rendered output:
//
//  1. Sibling-disjunction narrowing: when a discriminated step
//     (e.g. `name: "PRD"`) fails its own constraint, CUE first emits
//     one `type-mismatch` per OTHER branch of the disjunction it
//     tried (`conflicting values "TASK" and "PRD"`, etc). These
//     lines say "this isn't an UPDATE_DOCS step / this isn't a TASK
//     step" — true but useless when the operator already typed PRD.
//  2. Empty-disjunction placeholders: CUE wraps deep failures behind
//     `"<N> errors in empty disjunction:"` counter strings that have
//     no actionable content for callers.
//
// Filtering rules:
//
//  - When a (path)-group contains ≥1 entry whose code is one of
//    {invalid-pattern, constraint-violation, invalid-enum-value,
//    missing-required-field} AND ≥1 entry whose code is type-mismatch
//    matching `^conflicting values "[A-Z_]+" and "[A-Z_]+"$`, drop
//    every type-mismatch entry from that group (sibling narrowing).
//  - Always drop entries whose message matches
//    `^[0-9]+ errors in empty disjunction:?$` after we've already
//    extracted their leaf children via flattenCueErrors.
//
// The contract for callers: when there is a real constraint failure,
// FormatViolations renders ONLY the constraint failures. The fallback
// (no constraint failure surfaced) preserves the historical noisy
// output so operators don't lose information silently.
func FormatViolations(vs []Violation) string {
	if len(vs) == 0 {
		return ""
	}
	filtered := suppressDisjunctionNoise(vs)
	if len(filtered) == 0 {
		filtered = vs
	}
	var b strings.Builder
	for i, v := range filtered {
		if i > 0 {
			b.WriteByte('\n')
		}
		path := v.Path
		if path == "" {
			path = "<root>"
		}
		fmt.Fprintf(&b, "%s: %s at @addedIn(%s): %s", path, v.Code, v.AddedIn, v.Message)
	}
	return b.String()
}

// emptyDisjunctionRe matches CUE's empty-disjunction placeholder
// (`"3 errors in empty disjunction:"`). The trailing colon is
// optional so messages mid-flight (without the colon) still match.
var emptyDisjunctionRe = regexp.MustCompile(`^[0-9]+ errors in empty disjunction:?$`)

// siblingNarrowingRe matches the type-mismatch noise emitted when
// CUE narrows a discriminated-union member against its sibling
// branches: `conflicting values "PRD" and "TASK"` where both labels
// are upper-snake-case (the canonical step-name shape).
var siblingNarrowingRe = regexp.MustCompile(`^conflicting values "[A-Z_]+" and "[A-Z_]+"$`)

// nameEnumNoiseRe extracts the literal step name from the
// `invalid-enum-value` message that CUE emits at `…name` when one
// step branch already accepts the value but ANOTHER field on that
// branch fails. Shape:
//
//   name = "PRD" — must be one of [BRAINSTORMING, CODE_REVIEW, …]
//
// When the captured value (`PRD`) IS itself a valid step name from
// the SSOT allowlist, the row is sibling-narrowing noise — the
// operator typed a legal step type and the deeper failure is the
// actionable one. We drop these rows in pass 2.
var nameEnumNoiseRe = regexp.MustCompile(`^name = "([A-Z_]+)" — must be one of \[`)

// stepNameAllowlist is the in-package mirror of describe.go's
// ValidStepNames, kept here so the validator package doesn't need
// to import the cobra-attached describe-step-type surface. The
// `TestStepNameAllowlist_MirrorsDescribeAllowlist` test in
// describe_test.go fails if the two drift.
var stepNameAllowlist = map[string]bool{
	"BRAINSTORMING":         true,
	"PRD":                   true,
	"TASKS_MANIFEST":        true,
	"TASK":                  true,
	"CODE_REVIEW":           true,
	"RECEIVING_CODE_REVIEW": true,
	"WRITE_TESTS":           true,
	"UPDATE_DOCS":           true,
	"FEATURE_ACCEPTANCE":    true,
	"COMMIT":                true,
}

// isNameEnumNoise reports whether v is the misleading
// `invalid-enum-value` row at `…name` whose bad value IS itself a
// valid step name. These rows are emitted whenever CUE narrows the
// step-disjunction past a branch that already accepted the name —
// they are never actionable.
func isNameEnumNoise(v Violation) bool {
	if v.Code != "invalid-enum-value" {
		return false
	}
	if !strings.HasSuffix(v.Path, ".name") && v.Path != "name" {
		return false
	}
	m := nameEnumNoiseRe.FindStringSubmatch(v.Message)
	if m == nil {
		return false
	}
	return stepNameAllowlist[m[1]]
}

// constraintCodes is the set of violation codes that represent a
// real constraint failure (not narrowing noise, not a wrapper
// placeholder). When any of these appears in a (path)-group, the
// type-mismatch sibling-narrowing entries in that group are dropped.
var constraintCodes = map[string]bool{
	"invalid-pattern":        true,
	"constraint-violation":   true,
	"invalid-enum-value":     true,
	"missing-required-field": true,
}

// SuppressDisjunctionNoise applies the rules documented on
// FormatViolations. It returns a new slice and never mutates vs.
//
// Three passes:
//
//  1. Drop empty-disjunction placeholders (always — the leaves are
//     already extracted by flattenCueErrors).
//  2. Group by Path. If a group has a constraint failure AND at
//     least one sibling-narrowing type-mismatch, drop every
//     sibling-narrowing entry from that group. Real type-mismatch
//     errors (e.g. "expected string, got int") are preserved.
//  3. Drop misleading `invalid-enum-value` rows on `…name` whose
//     bad value is itself a valid step name (WF-CUE-NOISE-03,
//     2026-05-05) — covers the "PRD must be one of [...]" red
//     herring noted in the orchestrate-task-delivery retro 2026-05-05.
//     Only suppress when the parent step subtree carries a real
//     constraint failure, so a genuinely malformed `name` typo
//     still surfaces.
//
// Exported (capital S) since 2026-05-05 so the `validate` CLI render
// path can apply the same filter previously available only to the
// write-path's FormatViolations renderer.
//
// When the resulting slice is empty (e.g. the only violations were
// placeholders + sibling narrowing), the caller falls back to the
// raw input so we never silently render an empty error string.
func SuppressDisjunctionNoise(vs []Violation) []Violation {
	if len(vs) == 0 {
		return vs
	}
	// Pass 1: drop empty-disjunction placeholder rows.
	stage1 := make([]Violation, 0, len(vs))
	for _, v := range vs {
		if emptyDisjunctionRe.MatchString(v.Message) {
			continue
		}
		stage1 = append(stage1, v)
	}
	if len(stage1) == 0 {
		return stage1
	}
	// Pass 2 + 3: group by Path; suppress sibling narrowing AND
	// misleading name-enum noise when a real constraint failure
	// shares the parent step subtree. We compare against the Path's
	// step root (steps[N]) — the sibling narrowing happens at
	// `…name`, the real failure typically lives at a deeper subtree
	// (`…prd.acceptanceCriteria[1].bindsTo[0]`).
	hasConstraintByParent := map[string]bool{}
	for _, v := range stage1 {
		// A "real" constraint failure is either an explicit constraint
		// code OR a type-mismatch whose message is NOT the
		// sibling-narrowing pattern (so a genuine `conflicting values
		// "pass" and "pending"` enum failure still counts). The
		// name-enum noise itself is NOT a real failure for this
		// purpose — its presence does not unlock its own suppression.
		if isNameEnumNoise(v) {
			continue
		}
		isReal := constraintCodes[v.Code] ||
			(v.Code == "type-mismatch" && !siblingNarrowingRe.MatchString(v.Message))
		if !isReal {
			continue
		}
		parent := stepRootPath(v.Path)
		if parent == "" {
			continue
		}
		hasConstraintByParent[parent] = true
	}
	stage2 := make([]Violation, 0, len(stage1))
	for _, v := range stage1 {
		if v.Code == "type-mismatch" && siblingNarrowingRe.MatchString(v.Message) {
			parent := stepRootPath(v.Path)
			if parent != "" && hasConstraintByParent[parent] {
				continue
			}
		}
		if isNameEnumNoise(v) {
			parent := stepRootPath(v.Path)
			if parent != "" && hasConstraintByParent[parent] {
				continue
			}
		}
		stage2 = append(stage2, v)
	}
	return stage2
}

// suppressDisjunctionNoise is kept as a private alias to
// SuppressDisjunctionNoise so existing in-package call sites and
// tests don't need to be touched. New callers should use the
// exported name.
func suppressDisjunctionNoise(vs []Violation) []Violation {
	return SuppressDisjunctionNoise(vs)
}

// stepRootPath returns the closest enclosing step subtree path for a
// violation Path. The schema groups disjunction narrowing under
// `…steps[N]` (or `#WorkflowV1.steps[N]` for the embedded form), so
// we trim `path` back to that root. Returns "" when path doesn't
// reference a steps[] element — those violations are kept as-is.
func stepRootPath(path string) string {
	if path == "" {
		return ""
	}
	idx := strings.Index(path, "steps[")
	if idx < 0 {
		return ""
	}
	closing := strings.Index(path[idx:], "]")
	if closing < 0 {
		return ""
	}
	return path[:idx+closing+1]
}

// RecordNoSchemaCheck appends one audit line to
// `<repoRoot>/.browzer/audit/no-schema-check.log`. The line is:
//
//	<RFC3339-timestamp>\t<sha256-hex-of-payload>\t<verb>\t<path>
//
// Caller passes the absolute repo root so the audit dir resolution is
// deterministic — no walk-up, no env-var indirection. When the directory
// can't be created or the file can't be appended, the error is returned
// to the caller and the caller decides whether to abort the bypass (the
// bypass contract in WORKFLOW_SYNC_REDESIGN.md §6.2 says: bypass is only
// honoured if the audit succeeds).
func RecordNoSchemaCheck(repoRoot, verb, workflowPath string, payload []byte) error {
	if repoRoot == "" {
		return stderrors.New("schema: RecordNoSchemaCheck: repoRoot is empty")
	}
	dir := filepath.Join(repoRoot, ".browzer", "audit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("schema: create audit dir: %w", err)
	}
	logPath := filepath.Join(dir, "no-schema-check.log")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("schema: open audit log: %w", err)
	}
	defer func() { _ = f.Close() }()

	digest := sha256.Sum256(payload)
	line := fmt.Sprintf("%s\t%s\t%s\t%s\n",
		time.Now().UTC().Format(time.RFC3339),
		hex.EncodeToString(digest[:]),
		verb,
		workflowPath,
	)
	if _, werr := f.WriteString(line); werr != nil {
		return fmt.Errorf("schema: write audit log: %w", werr)
	}
	return nil
}

// FindRepoRoot walks up from start to find a `.git` dir or fallback to
// the cwd. Used by the CLI to anchor the no-schema-check audit log; when
// no repo root is found, the log is written under the workflow.json's
// directory's `.browzer/audit/`.
//
// findRepoRootMaxDepth caps the walk-up at 32 directories — a defensive
// upper bound that comfortably exceeds any realistic checkout depth
// while preventing infinite loops on unusual filesystems (e.g. cyclic
// bind mounts). F-SE-8 (2026-05-04): name lifted from a magic number
// to a documented constant.
const findRepoRootMaxDepth = 32

func FindRepoRoot(start string) string {
	cur := start
	for range findRepoRootMaxDepth {
		if cur == "" || cur == "/" {
			break
		}
		if st, err := os.Stat(filepath.Join(cur, ".git")); err == nil && st.IsDir() {
			return cur
		}
		next := filepath.Dir(cur)
		if next == cur {
			break
		}
		cur = next
	}
	return start
}

// =============================================================
// internal helpers — not exported.
// =============================================================

// fieldNotAllowedRe matches CUE's "field not allowed" message — the
// parent struct is closed and the operator typed an undeclared key. We
// translate this to the stable `unknown-field` code AND collect the
// closed-struct's accepted field labels via `lookupAllowedFieldsForPath`.
var fieldNotAllowedRe = regexp.MustCompile(`field not allowed`)

// conflictingValuesRe extracts the operand strings from CUE's
// `conflicting values "X" and "Y"` line so the enricher can decide
// whether the failing value sits in a string disjunction.
var conflictingValuesRe = regexp.MustCompile(`^conflicting values\s+"[^"]*"\s+and\s+"[^"]*"$`)

// conflictingValueExtractRe pulls the candidate operand strings out of
// `conflicting values "X" and "Y"` so the enricher can collect each
// `X` across sibling violations on the same Path. We treat `X` as a
// legal-enum candidate and `Y` as the operator-supplied bad value.
var conflictingValueExtractRe = regexp.MustCompile(`^conflicting values\s+"([^"]*)"\s+and\s+"([^"]*)"$`)

// incompatibleListLengthsRe matches CUE's "incompatible list lengths
// (M and N)" message — emitted when a payload submits an array of the
// wrong shape against a `*[] | [...#X]` disjunction. The empty-default
// branch (`*[]`) trips on length mismatch and the open branch trips on
// element shape; the user typically sees BOTH messages stacked. The
// list-lengths variant by itself is cryptic ("incompatible list
// lengths (0 and 7)" tells the operator nothing about the expected
// element shape) so we reclassify and rewrite when the schema can
// resolve the array's element struct fields.
var incompatibleListLengthsRe = regexp.MustCompile(
	`^incompatible list lengths\s+\((\d+)\s+and\s+(\d+)\)$`,
)

// enrichViolations walks each Violation and attaches `AllowedFields` /
// `AllowedValues` derived from the schema and from sibling-violation
// inference. Designed so a future change to the CUE engine's message
// formatting only fails enrichment for the affected entry — the
// surrounding pipeline still emits a useful violation (just without
// the hint slice).
//
// Mutations are in-place. Two passes:
//
//  1. Pre-pass: collect string-disjunction candidates from sibling
//     `conflicting values "X" and "Y"` lines on the same Path. Three
//     sibling violations like
//       conflicting values "fail" and "pending"
//       conflicting values "pass" and "pending"
//       conflicting values "skip" and "pending"
//     unambiguously declare `[fail, pass, skip]` as the disjunction
//     and `"pending"` as the bad value. We use this when schema-walk
//     can't reach the field (typically because the path crosses a
//     discriminated-union narrowing point).
//  2. Per-violation pass:
//      - Promote "field not allowed" rows to `unknown-field` and
//        resolve the parent struct's allowed field labels.
//      - Reclassify type-mismatch rows whose Path resolves to a
//        string disjunction → `invalid-enum-value` with AllowedValues
//        populated from the schema (preferred) or the sibling
//        inference (fallback).
//      - Existing `invalid-enum-value` rows get AllowedValues filled
//        in from either source.
//
// The function tolerates an empty schema value: every step
// short-circuits when the schema-walk returns nothing usable.
func enrichViolations(vs []Violation, schemaRoot cue.Value) {
	if len(vs) == 0 {
		return
	}
	siblingEnums := collectSiblingEnumCandidates(vs)
	for i := range vs {
		v := &vs[i]
		// Pass 1: detect "field not allowed" → unknown-field with allowedFields.
		if fieldNotAllowedRe.MatchString(v.Message) {
			v.Code = "unknown-field"
			parent := parentSchemaPath(v.Path)
			if schemaRoot.Exists() {
				if fields := lookupAllowedFieldsForPath(schemaRoot, parent); len(fields) > 0 {
					v.AllowedFields = fields
				}
			}
			continue
		}
		// Pass 2: type-mismatch on a string disjunction → invalid-enum-value.
		if v.Code == "type-mismatch" && conflictingValuesRe.MatchString(v.Message) {
			values := lookupEnumValuesForPathOrSibling(schemaRoot, v.Path, siblingEnums)
			if len(values) > 0 {
				v.Code = "invalid-enum-value"
				v.AllowedValues = values
				bad := extractBadValue(v.Message)
				v.Message = renderEnumValueMismatchMessage(v.Field, values, bad)
				continue
			}
		}
		// Pass 3: existing invalid-enum-value rows benefit from the same
		// hint when CUE's default message left allowedValues empty.
		if v.Code == "invalid-enum-value" && len(v.AllowedValues) == 0 {
			if values := lookupEnumValuesForPathOrSibling(schemaRoot, v.Path, siblingEnums); len(values) > 0 {
				v.AllowedValues = values
			}
		}
		// Pass 4: array-shape-mismatch — promote CUE's "incompatible list
		// lengths" to a structured diagnostic that names the expected
		// element struct fields. Retro 2026-05-05 §2.3: the raw CUE
		// message ("incompatible list lengths (0 and 7)") tells the
		// operator nothing about WHY the array shape is wrong; resolving
		// the schema's element type lets us render
		// `successMetrics: expected array of objects with fields {id,
		// metric, target, method}` instead. When the schema can't be
		// resolved (path missing, element isn't a struct, no labelled
		// fields) we still reclassify but leave the message verbatim so
		// the operator at least sees a stable Code.
		if match := incompatibleListLengthsRe.FindStringSubmatch(v.Message); len(match) == 3 {
			v.Code = "array-shape-mismatch"
			if schemaRoot.Exists() {
				if fields := lookupArrayElementFieldsForPath(schemaRoot, v.Path); len(fields) > 0 {
					v.AllowedFields = fields
					v.Message = renderArrayShapeMismatchMessage(v.Field, fields, match[2])
				}
			}
			continue
		}
	}
}

// lookupArrayElementFieldsForPath walks schemaRoot to the array value at
// `path` and returns the alpha-sorted list of declared field labels for
// the array's element type.
//
// Resolves `*[] | [...#X]` style disjunctions by walking through to the
// open-list branch via cue.AnyIndex, then through any
// `*null | <struct>` style element disjunctions before listing fields.
//
// Walks through the per-step definition map when the path starts with
// `steps[N].` and the direct walk through #WorkflowV1 returns nothing —
// CUE cannot resolve a `prd.…` lookup through the discriminated step
// union without knowing WHICH step type is targeted, so we try each
// declared step definition (#PRDStep, #TaskStep, …) in turn and return
// the first one whose subpath resolves to a non-empty struct array.
// This is the read-only mirror of `repairStepLeafViolations`'s walk.
//
// Returns nil when:
//   - the path doesn't resolve through any of the candidate roots,
//   - the value isn't an array (cue.AnyIndex lookup misses),
//   - the element type isn't a struct,
//   - the struct has no labeled fields (open `{...}`).
func lookupArrayElementFieldsForPath(schemaRoot cue.Value, path string) []string {
	if labels := lookupArrayElementFieldsAt(schemaRoot, path); len(labels) > 0 {
		return labels
	}
	// Fallback: try each per-step definition when the path crosses the
	// steps[N] discriminated union.
	subpath := stripStepsIndexPrefix(path)
	if subpath == "" {
		return nil
	}
	for _, defName := range stepNameToDefinition {
		defVal, ok := lookupDefinition(defName)
		if !ok {
			continue
		}
		if labels := lookupArrayElementFieldsAt(defVal, subpath); len(labels) > 0 {
			return labels
		}
	}
	return nil
}

// lookupArrayElementFieldsAt is the schema-walk-then-extract core used by
// lookupArrayElementFieldsForPath against a chosen schema root.
//
// The walker explicitly iterates list-disjunction branches because the
// SSOT pattern `*[] | [...#X]` puts the empty-default first; a plain
// `LookupPath(cue.AnyIndex)` on the disjunction falls through to the
// default branch (an empty list with no element type) and returns
// bottom. Iterating the disjunction lets the open-list branch surface
// its #X element.
func lookupArrayElementFieldsAt(root cue.Value, path string) []string {
	v, ok := walkSchemaPath(root, path)
	if !ok {
		return nil
	}
	for _, branch := range listDisjunctionBranches(v) {
		elem := branch.LookupPath(cue.MakePath(cue.AnyIndex))
		if !elem.Exists() {
			continue
		}
		if resolved := resolveStructFromDisjunction(elem); resolved != nil {
			elem = *resolved
		}
		fields, err := elem.Fields(cue.All())
		if err != nil {
			continue
		}
		var labels []string
		for fields.Next() {
			labels = append(labels, fields.Selector().Unquoted())
		}
		if len(labels) == 0 {
			continue
		}
		sort.Strings(labels)
		return labels
	}
	return nil
}

// listDisjunctionBranches returns the operand list of a list value's
// underlying expression: both branches when v is a disjunction
// (`*[] | [...#X]` → 2 branches), the single concrete-list arg when
// CUE pre-collapsed the disjunction during compilation (NoOp + 1 arg
// — the common case for our SSOT pattern, where v.Kind() reads as
// `_|_` because the default `*[]` made v non-concrete but the open
// branch `[...#X]` carries the element type), or a singleton {v} for
// values that don't decompose at all.
//
// CUE's `cue.Value.Expr()` returns the AST shape after schema-side
// evaluation: a `*[] | [...#X]` field reads back as `op=NoOp args=[<the
// open list>]` rather than `op=OrOp args=[*[], [...#X]]` because the
// default branch is folded into the `*` marker on the value itself.
// The walker treats both shapes uniformly: anything with at least one
// expression arg is iterated; falls back to the bare value for the
// no-arg case.
func listDisjunctionBranches(v cue.Value) []cue.Value {
	op, args := v.Expr()
	if len(args) == 0 {
		return []cue.Value{v}
	}
	switch op {
	case cue.OrOp, cue.NoOp:
		return args
	default:
		return []cue.Value{v}
	}
}

// stripStepsIndexPrefix returns the substring of `path` AFTER the
// leading `steps[N].` segment. Returns "" when the path doesn't start
// with `steps[`. Used to translate workflow-rooted paths (e.g.
// `steps[0].prd.successMetrics`) into per-step-definition subpaths
// (`prd.successMetrics`) so the array-element walker can fall back
// through the step-type definitions.
//
// Also strips leading CUE definition prefixes like `#WorkflowV1.` so
// paths emitted by `repairStepLeafViolations`
// (`#WorkflowV1.steps[1].prd.successMetrics`) resolve identically to
// the canonical `steps[1].prd.successMetrics` form. The strip mirrors
// `walkSchemaPath`'s `if strings.HasPrefix(p, "#") { continue }` rule.
func stripStepsIndexPrefix(path string) string {
	// Strip any leading `#Definition.` segment(s) to normalize
	// workflow-rooted paths emitted by repairStepLeafViolations.
	for strings.HasPrefix(path, "#") {
		dot := strings.Index(path, ".")
		if dot < 0 {
			return ""
		}
		path = path[dot+1:]
	}
	if !strings.HasPrefix(path, "steps[") {
		return ""
	}
	closing := strings.Index(path, "]")
	if closing < 0 {
		return ""
	}
	rest := path[closing+1:]
	return strings.TrimPrefix(rest, ".")
}

// renderArrayShapeMismatchMessage emits a stable diagnostic that names
// the expected struct field set so the operator can fix the payload
// shape on the first retry. The `gotCount` argument is the second
// operand of the original CUE "incompatible list lengths (M and N)"
// message — preserving it in the output keeps the line greppable for
// existing stderr-watchers.
//
// Example output for `successMetrics`:
//
//	`successMetrics: expected array of objects with fields {id, metric,
//	target, method} (got incompatible array shape; received 7 element(s))`
func renderArrayShapeMismatchMessage(field string, fields []string, gotCount string) string {
	if field == "" {
		field = "value"
	}
	suffix := "got incompatible array shape"
	if gotCount != "" {
		suffix = fmt.Sprintf("got incompatible array shape; received %s element(s)", gotCount)
	}
	return fmt.Sprintf(
		"%s: expected array of objects with fields {%s} (%s)",
		field, strings.Join(fields, ", "), suffix,
	)
}

// collectSiblingEnumCandidates groups violations by Path and, for each
// path that has ≥2 sibling `conflicting values "X" and "Y"` rows
// AGREEING on Y (the bad value), collects the X values as the
// disjunction membership. Returns a Path → sorted []string map. When
// the Y operand differs across siblings, the path is skipped — that
// pattern means the engine is reporting multiple distinct mismatches,
// not a disjunction.
func collectSiblingEnumCandidates(vs []Violation) map[string][]string {
	type bucket struct {
		bad   string
		legal map[string]bool
		mixed bool
	}
	groups := map[string]*bucket{}
	for _, v := range vs {
		if v.Code != "type-mismatch" {
			continue
		}
		match := conflictingValueExtractRe.FindStringSubmatch(v.Message)
		if len(match) != 3 {
			continue
		}
		legal, bad := match[1], match[2]
		b, ok := groups[v.Path]
		if !ok {
			b = &bucket{bad: bad, legal: map[string]bool{legal: true}}
			groups[v.Path] = b
			continue
		}
		if b.bad != bad {
			b.mixed = true
			continue
		}
		b.legal[legal] = true
	}
	out := map[string][]string{}
	for path, b := range groups {
		if b.mixed || len(b.legal) < 2 {
			continue
		}
		legal := make([]string, 0, len(b.legal))
		for s := range b.legal {
			legal = append(legal, s)
		}
		sort.Strings(legal)
		out[path] = legal
	}
	return out
}

// lookupEnumValuesForPathOrSibling tries the schema-walk first;
// when that returns nothing it falls back to the sibling-inference
// table populated by collectSiblingEnumCandidates. Either source
// yields an alpha-sorted slice (or nil).
func lookupEnumValuesForPathOrSibling(schemaRoot cue.Value, path string, sibling map[string][]string) []string {
	if schemaRoot.Exists() {
		if values := lookupEnumValuesForPath(schemaRoot, path); len(values) > 0 {
			return values
		}
	}
	return sibling[path]
}

// renderEnumValueMismatchMessage emits a stable message that the
// FormatViolations pipeline can render verbatim while still being
// greppable by downstream skills. When `bad` is non-empty the value
// the operator supplied is preserved in the message so existing
// stderr-greppers (`grep '"pending"'`) keep working:
//
//	bad set:    `<field> = "<bad>" — must be one of [v1, v2, v3]`
//	bad empty:  `<field> must be one of [v1, v2, v3]`
func renderEnumValueMismatchMessage(field string, values []string, bad string) string {
	if field == "" {
		field = "value"
	}
	if bad != "" {
		return fmt.Sprintf("%s = %q — must be one of [%s]",
			field, bad, strings.Join(values, ", "))
	}
	return fmt.Sprintf("%s must be one of [%s]", field, strings.Join(values, ", "))
}

// extractBadValue returns the second operand of a `conflicting values
// "X" and "Y"` line — the value the operator actually supplied.
// Returns "" when the message is not in that format.
func extractBadValue(msg string) string {
	m := conflictingValueExtractRe.FindStringSubmatch(msg)
	if len(m) != 3 {
		return ""
	}
	return m[2]
}

// parentSchemaPath strips the trailing path segment so the enricher can
// walk the parent struct's accepted fields. For `steps[0].task.foo`
// returns `steps[0].task`. Returns "" when path has no separator.
func parentSchemaPath(path string) string {
	if path == "" {
		return ""
	}
	if idx := strings.LastIndex(path, "."); idx >= 0 {
		return path[:idx]
	}
	return ""
}

// lookupAllowedFieldsForPath walks schemaRoot to the struct value at
// `path` and returns the alpha-sorted list of declared field labels.
// The walk follows the same dotted/`[N]` form joinCuePath emits.
//
// Returns nil when the path can't be resolved or the value at path is
// not a struct — never panics.
func lookupAllowedFieldsForPath(schemaRoot cue.Value, path string) []string {
	v, ok := walkSchemaPath(schemaRoot, path)
	if !ok {
		return nil
	}
	// Resolve through `*null | <struct>` disjunctions just like the
	// describe.go walker.
	if resolved := resolveStructFromDisjunction(v); resolved != nil {
		v = *resolved
	}
	fields, err := v.Fields(cue.All())
	if err != nil {
		return nil
	}
	var labels []string
	for fields.Next() {
		labels = append(labels, fields.Selector().Unquoted())
	}
	if len(labels) == 0 {
		return nil
	}
	sort.Strings(labels)
	return labels
}

// lookupEnumValuesForPath walks schemaRoot to the value at `path` and,
// when the value is a string disjunction (`"a" | "b" | "c"` possibly
// default-marked), returns the alpha-sorted list of literal members.
// Returns nil when the value is not a string disjunction.
func lookupEnumValuesForPath(schemaRoot cue.Value, path string) []string {
	v, ok := walkSchemaPath(schemaRoot, path)
	if !ok {
		return nil
	}
	return extractStringEnum(v)
}

// walkSchemaPath descends schemaRoot following the dotted joinCuePath
// form (`steps[0].task.foo`). Array selectors are resolved via
// `cue.AnyIndex` so any element of a list type works (CUE typically
// declares lists as `[...#Element]` so element constraints apply
// uniformly). Returns (cue.Value{}, false) when any segment is missing.
//
// CUE prefixes paths with the discriminator definition name in the
// emitted error tree (e.g. `#WorkflowV1.steps[0].…`); the prefix is
// stripped because schemaRoot already IS the resolved #WorkflowV1
// value. Same logic applies to the per-step definitions surfaced
// through repairStepLeafViolations (`#TaskStep.task.execution.…`).
func walkSchemaPath(schemaRoot cue.Value, path string) (cue.Value, bool) {
	if path == "" {
		return schemaRoot, true
	}
	cur := schemaRoot
	parts := splitDottedPath(path)
	for _, p := range parts {
		// Drop the leading definition selector if present — the
		// caller already passed the resolved root.
		if strings.HasPrefix(p, "#") {
			continue
		}
		// Skip array indices: CUE's struct walk treats list-element
		// types as `[...#T]` reachable via `cue.AnyIndex`.
		if isAllDigits(p) {
			next := cur.LookupPath(cue.MakePath(cue.AnyIndex))
			if !next.Exists() {
				return cue.Value{}, false
			}
			cur = next
			continue
		}
		// Resolve disjunction to its struct branch BEFORE descending.
		if resolved := resolveStructFromDisjunction(cur); resolved != nil {
			cur = *resolved
		}
		next := cur.LookupPath(cue.ParsePath(p))
		if !next.Exists() {
			return cue.Value{}, false
		}
		cur = next
	}
	return cur, true
}

// splitDottedPath splits `steps[0].task.foo` → `["steps","0","task","foo"]`.
// Array indices become bare digit segments so walkSchemaPath can apply
// `cue.AnyIndex` for them.
func splitDottedPath(path string) []string {
	var out []string
	cur := strings.Builder{}
	flush := func() {
		s := cur.String()
		if s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(path); i++ {
		ch := path[i]
		switch ch {
		case '.':
			flush()
		case '[':
			flush()
			// Read until ']'.
			j := i + 1
			for j < len(path) && path[j] != ']' {
				j++
			}
			if j > i+1 {
				out = append(out, path[i+1:j])
			}
			i = j
		default:
			cur.WriteByte(ch)
		}
	}
	flush()
	return out
}

// convertCueErrors lifts cue.Error into our flat Violation slice.
// Each cue.Error path/message becomes one Violation. Heuristics map error
// kinds to stable Code values.
//
// WF-CUE-NOISE-02 (2026-05-04): when CUE rejects a deeply nested
// disjunction it wraps leaf failures behind one or more
// `"<N> errors in empty disjunction:"` placeholders. The flat
// `cueerrors.Errors(vErr)` call only returns the outer list — the leaf
// constraint failures (e.g. `invalid value "pending"`) live inside an
// inner `list` reachable by re-running `cueerrors.Errors` on the
// placeholder entry. We walk the tree breadth-first, tracking visited
// errors by identity to terminate on leaves (where a single-element
// `Errors(entry)` returns the entry itself).
func convertCueErrors(vErr error, addedIn map[string]string) []Violation {
	if vErr == nil {
		return nil
	}
	leaves := flattenCueErrors(vErr)
	if len(leaves) == 0 {
		// Fallback: treat the raw error as a single root-level violation
		// so callers always see at least one entry to act on.
		return []Violation{{
			Path:    "",
			Code:    "unknown-error",
			Message: vErr.Error(),
			AddedIn: defaultAddedIn,
		}}
	}
	out := make([]Violation, 0, len(leaves))
	for _, ce := range leaves {
		path := joinCuePath(ce.Path())
		msg := errMessage(ce)
		v := Violation{
			Path:    path,
			Code:    classifyCode(msg),
			Message: msg,
			AddedIn: lookupAddedIn(addedIn, path),
			Field:   trailingField(path),
		}
		out = append(out, v)
	}
	return out
}

// flattenCueErrors recursively descends CUE's nested disjunction error
// tree and returns the union of every leaf error encountered. The CUE
// engine wraps inner-disjunction failures behind a placeholder error
// whose message is the literal `"<N> errors in empty disjunction:"`;
// `cueerrors.Errors(placeholder)` returns the inner list, so we walk
// the worklist until no new errors surface.
//
// Termination: an error is "expanded" only when `cueerrors.Errors(e)`
// returns at least one element distinct from `e` itself (identity
// compare). A leaf error returns `[e]` and is added to the result
// without further recursion.
func flattenCueErrors(root error) []cueerrors.Error {
	if root == nil {
		return nil
	}
	seen := map[cueerrors.Error]bool{}
	var out []cueerrors.Error
	queue := append([]cueerrors.Error(nil), cueerrors.Errors(root)...)
	for len(queue) > 0 {
		e := queue[0]
		queue = queue[1:]
		if e == nil || seen[e] {
			continue
		}
		seen[e] = true
		children := cueerrors.Errors(e)
		// Leaf: Errors(e) returns just [e] (single self-reference) or
		// nothing. Either way, no further descent is meaningful.
		if len(children) == 0 || (len(children) == 1 && children[0] == e) {
			out = append(out, e)
			continue
		}
		// Multi-error wrapper: recurse into children.
		for _, c := range children {
			if c == nil || c == e || seen[c] {
				continue
			}
			queue = append(queue, c)
		}
		// Also keep the wrapper entry itself if it carries an
		// otherwise-unobservable message — this preserves the
		// placeholder line for back-compat with operators who relied
		// on it for context. Suppression of placeholder noise lives
		// in FormatViolations (WF-CUE-NOISE-01/02).
		out = append(out, e)
	}
	return out
}

// joinCuePath converts CUE's []string path to a dotted JSON Pointer-like
// string. CUE uses bare segments like `steps`, `0`, `task`, `name`; we
// emit `steps[0].task.name`.
func joinCuePath(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	for i, p := range parts {
		// Numeric segments → array index.
		if isAllDigits(p) {
			b.WriteByte('[')
			b.WriteString(p)
			b.WriteByte(']')
			continue
		}
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(p)
	}
	return b.String()
}

// isAllDigits is a hot-path helper.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// errMessage extracts the human-readable message from a cue.Error.
// Falls through to .Error() when no Format() output is available.
func errMessage(ce cueerrors.Error) string {
	format, args := ce.Msg()
	if format != "" {
		return fmt.Sprintf(format, args...)
	}
	return ce.Error()
}

// classifyCode maps a CUE error message to a stable Code identifier.
// Heuristics — keep them additive; downstream skills must tolerate
// unknown codes via the catch-all `unknown-error`.
func classifyCode(msg string) string {
	low := strings.ToLower(msg)
	// F-SE-1 / F-12 (2026-05-04): explicit parens around `&&` sub-expressions
	// to defeat Go's operator precedence (`&&` binds tighter than `||`). The
	// previous form `... || "field not allowed" && "required"` parsed as
	// `... || ("field not allowed" && "required")`, but the intent is clearer
	// and safer when the conjunction is parenthesized.
	switch {
	case strings.Contains(low, "incomplete value") ||
		strings.Contains(low, "field is required") ||
		strings.Contains(low, "missing field") ||
		(strings.Contains(low, "field not allowed") &&
			strings.Contains(low, "required")):
		return "missing-required-field"
	case strings.Contains(low, "invalid value") &&
		(strings.Contains(low, "or ") || strings.Contains(low, "disjunction")):
		return "invalid-enum-value"
	case strings.Contains(low, "does not match") ||
		strings.Contains(low, "no match") ||
		strings.Contains(low, "regex"):
		return "invalid-pattern"
	case strings.Contains(low, "conflicting values") ||
		(strings.Contains(low, "expected") && strings.Contains(low, "got")):
		return "type-mismatch"
	case strings.Contains(low, "structural"):
		return "structural-error"
	case strings.Contains(low, "out of bound") ||
		strings.Contains(low, ">=") ||
		strings.Contains(low, "<="):
		return "constraint-violation"
	default:
		return "unknown-error"
	}
}

// trailingField returns the last identifier in the path. For
// `steps[0].task.scope[0]` returns `scope`; for `kind` returns `kind`.
func trailingField(path string) string {
	if path == "" {
		return ""
	}
	last := path
	if i := strings.LastIndex(last, "."); i >= 0 {
		last = last[i+1:]
	}
	if i := strings.Index(last, "["); i >= 0 {
		last = last[:i]
	}
	return last
}

// lookupAddedIn walks the @addedIn map for the closest matching schema
// path. Strategy: try the full path, then strip trailing array indices
// (`[0]`, `[1]`, ...), then strip path components from the right until
// we hit a known field. Default to defaultAddedIn when nothing matches.
func lookupAddedIn(m map[string]string, path string) string {
	if path == "" {
		return defaultAddedIn
	}
	// Strip array indices for lookup — the schema is index-agnostic.
	noIdx := arrayIndexRe.ReplaceAllString(path, "")
	if v, ok := m[noIdx]; ok && v != "" {
		return v
	}
	// Walk up the dotted path.
	cur := noIdx
	for cur != "" {
		if v, ok := m[cur]; ok && v != "" {
			return v
		}
		idx := strings.LastIndex(cur, ".")
		if idx < 0 {
			break
		}
		cur = cur[:idx]
	}
	// Final fallback: trailing field key only.
	if last := trailingField(path); last != "" {
		if v, ok := m[last]; ok && v != "" {
			return v
		}
	}
	return defaultAddedIn
}

var arrayIndexRe = regexp.MustCompile(`\[\d+\]`)

// addedInLineRe matches `<field>:` ... `@addedIn("<iso>")` on a single
// line of the SSOT. Captures field name + ISO.
var addedInLineRe = regexp.MustCompile(`(?m)^\s*([A-Za-z_][A-Za-z0-9_]*)[?!]?\s*:.*@addedIn\("([^"]+)"\)`)

// parseAddedInMap is a cheap line-scan over the SSOT to map field names
// to their @addedIn ISO. We don't disambiguate between definitions (a
// field name `kind` exists under #ScopeAdjustment AND #Warning); the
// lookup falls back to the trailing-field key, which is good enough for
// the audit-line format. A future revision can wire the CUE AST to
// produce a per-path map.
func parseAddedInMap(src string) map[string]string {
	out := map[string]string{}
	matches := addedInLineRe.FindAllStringSubmatch(src, -1)
	for _, m := range matches {
		field := m[1]
		ts := m[2]
		// Last @addedIn wins for a duplicate field name.
		out[field] = ts
	}
	return out
}

// sortViolations enforces the deterministic order used by
// FormatViolations + the audit-line emission. (Path, Code, Message).
func sortViolations(vs []Violation) {
	sort.SliceStable(vs, func(i, j int) bool {
		if vs[i].Path != vs[j].Path {
			return vs[i].Path < vs[j].Path
		}
		if vs[i].Code != vs[j].Code {
			return vs[i].Code < vs[j].Code
		}
		return vs[i].Message < vs[j].Message
	})
}
