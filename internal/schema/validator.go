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
type Violation struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
	AddedIn string `json:"addedIn"`
	Field   string `json:"field"`
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
	sortViolations(violations)
	if violations == nil {
		violations = []Violation{}
	}
	return ValidationResult{Valid: len(violations) == 0, Violations: violations}
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
func FormatViolations(vs []Violation) string {
	if len(vs) == 0 {
		return ""
	}
	var b strings.Builder
	for i, v := range vs {
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

// convertCueErrors lifts cue.Error into our flat Violation slice.
// Each cue.Error path/message becomes one Violation. Heuristics map error
// kinds to stable Code values.
func convertCueErrors(vErr error, addedIn map[string]string) []Violation {
	if vErr == nil {
		return nil
	}
	cueErrs := cueerrors.Errors(vErr)
	if len(cueErrs) == 0 {
		// Fallback: treat the raw error as a single root-level violation
		// so callers always see at least one entry to act on.
		return []Violation{{
			Path:    "",
			Code:    "unknown-error",
			Message: vErr.Error(),
			AddedIn: defaultAddedIn,
		}}
	}
	out := make([]Violation, 0, len(cueErrs))
	for _, ce := range cueErrs {
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
