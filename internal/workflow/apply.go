// Package workflow — apply.go
//
// Shared write core for workflow.json mutations. Both the daemon's
// WorkflowMutate goroutine and the standalone CLI fallback path call
// ApplyAndPersist so there is exactly one place that owns the
// load → mutate → validate → marshal → atomic-write → fsync pipeline.
//
// Lock ownership: the CALLER acquires/releases the advisory lock around
// ApplyAndPersist. ApplyAndPersist never touches the lock.
//
// Pipeline (executed under the caller's lock):
//  1. Read the file from disk into raw map.
//  2. Run the verb's Mutator over the raw map. Mutator may set ApplyResult.StepID.
//  3. Marshal the mutated map back to JSON bytes.
//  4. json.Unmarshal those bytes into a typed Workflow.
//  5. Run Validate(typed). Validation failures abort BEFORE any write.
//  6. json.MarshalIndent the mutated map and write atomically via AtomicWrite.
//  7. If awaitDurability=true, fsync the file AND its parent directory.
//
// Error semantics:
//  - load / parse / mutator / validation errors leave the file untouched.
//  - rename failure inside AtomicWrite leaves the original file intact.
//  - fsync failures after a successful rename are returned but the file is
//    already replaced; durability is "best-effort but reported".
package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/browzeremb/browzer-cli/internal/schema"
)

// ApplyResult carries the output of one ApplyAndPersist call.
type ApplyResult struct {
	// StepID is the workflow step affected by the mutation. May be empty
	// for verbs that target the workflow document itself (set-config, patch).
	StepID string
	// ValidatedOk is true iff Validate(typed) returned no errors.
	ValidatedOk bool
	// Durable is true iff awaitDurability=true was honored end-to-end (file
	// fsync + parent dir fsync both succeeded). For awaitDurability=false the
	// field is always false even when the kernel ends up flushing the page.
	Durable bool
	// NoOp is true when the mutator decided no change was needed (e.g.
	// complete-step on an already-COMPLETED step). When true ApplyAndPersist
	// SKIPS the marshal+write+fsync entirely so the file's bytes-on-disk
	// remain bit-identical and downstream tooling (git, fsnotify, parsers
	// that compare hashes) doesn't see a spurious touch.
	NoOp bool
	// NoOpReason is set together with NoOp=true to give the audit line a
	// human-readable explanation. Empty when NoOp=false.
	NoOpReason string
}

// Mutator is the in-place transform applied to the raw workflow map under
// the caller's lock. The Mutator MUST set out.StepID when the verb is
// step-scoped so the caller's audit line can carry it.
//
// The payload is the raw JSON body the caller passed (CLI: --payload file
// or stdin; daemon: WorkflowMutateParams.Payload). Verbs that do not need a
// payload ignore it.
//
// args carries verb-specific positional arguments (e.g. set-status takes
// stepId+status, patch takes the jq expression). Keeping these typed-as-args
// instead of leaking cobra/flag types keeps the Mutator surface portable.
type Mutator func(raw map[string]any, args MutatorArgs, out *ApplyResult) error

// MutatorArgs carries the per-call inputs that are not part of the workflow
// document itself: positional args, the JSON payload bytes, and the verb's
// jq expression for `patch`.
type MutatorArgs struct {
	// Args are positional arguments after the verb (e.g. ["step-1","RUNNING"]
	// for set-status step-1 RUNNING).
	Args []string
	// Payload is the raw payload bytes. Used by append-step and
	// append-review-history. Empty for the other verbs.
	Payload []byte
	// JQExpr is the jq mutation expression. Used only by `patch`.
	JQExpr string
	// JQVars binds variables for the `patch` jq expression — the gojq
	// equivalent of `jq --arg name value` / `jq --argjson name <json>`.
	// Keys are bare identifiers (no leading `$`); values are Go-native
	// JSON-decoded scalars/maps/arrays. Used only by `patch`.
	JQVars map[string]any

	// NoSchemaCheck bypasses CUE-based schema validation in
	// ApplyAndPersist (TASK_02 / WF-SYNC-1). When true the validator is
	// skipped AND a line is appended to
	// `<repo-root>/.browzer/audit/no-schema-check.log` (timestamp + sha256
	// digest of the rejected payload) so the bypass is auditable.
	//
	// Daemon path: TASK_06 plumbs --no-schema-check through the JSON-RPC
	// surface; until then, the daemon ALWAYS validates regardless of this
	// field. CLI standalone path honours it immediately.
	NoSchemaCheck bool
	// AuditRepoRoot is the absolute path to the repo root used to anchor
	// the no-schema-check audit log. When empty, ApplyAndPersist falls
	// back to FindRepoRoot(filepath.Dir(workflowPath)).
	AuditRepoRoot string

	// WorkflowDir is the directory containing the workflow.json being
	// mutated. Auto-populated by ApplyAndPersist from filepath.Dir(path);
	// callers using ApplyToRaw directly may set it explicitly. Used by
	// mutators that maintain side-channel cache files like
	// .browzer/active-step (TASK_05 hybrid-cache).
	WorkflowDir string
}

// Mutators is the verb registry. Keep keys in sync with the cobra subcommand
// names — daemon and skills both call by verb string.
var Mutators = map[string]Mutator{
	"append-dispatch":            mutatorAppendDispatch,
	"append-step":               mutatorAppendStep,
	"update-step":               mutatorUpdateStep,
	"complete-step":             mutatorCompleteStep,
	"set-status":                mutatorSetStatus,
	"set-config":                mutatorSetConfig,
	"append-review-history":     mutatorAppendReviewHistory,
	"set-current-step":          mutatorSetCurrentStep,
	"patch":                     mutatorPatch,
	"reapply-additional-context": mutatorReapplyAdditionalContext,
	"audit-model-override":      mutatorAuditModelOverride,
	"truncation-audit":          mutatorTruncationAudit,
}

// ErrUnknownVerb is returned by ApplyAndPersist when verb is not in Mutators.
var ErrUnknownVerb = errors.New("workflow: unknown verb")

// ApplyAndPersist runs the full mutate-and-write pipeline for a single
// workflow.json mutation. The caller MUST hold the advisory lock for the
// duration of this call.
//
// awaitDurability=true triggers an explicit f.Sync() on the freshly written
// file AND a Sync() on the containing directory so the rename and contents
// survive a power loss. awaitDurability=false relies on the OS page cache
// (the historic CLI behaviour pre-2026-04-29).
//
// Idempotency: per-verb mutators encode their own idempotency rules (e.g.
// complete-step on an already-completed step is a no-op + no validation
// regression). When a Mutator decides "nothing to do", it returns nil with
// out.ValidatedOk=true and out.StepID set; the caller still emits an audit
// line marking the no-op.
func ApplyAndPersist(path, verb string, args MutatorArgs, awaitDurability bool) (ApplyResult, error) {
	mut, ok := Mutators[verb]
	if !ok {
		return ApplyResult{}, fmt.Errorf("%w: %q", ErrUnknownVerb, verb)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("read workflow: %w", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return ApplyResult{}, fmt.Errorf("parse workflow map: %w", err)
	}
	if raw == nil {
		raw = map[string]any{}
	}

	// Populate WorkflowDir so mutators can write side-channel cache files
	// (TASK_05 .browzer/active-step). Callers that set this explicitly win.
	if args.WorkflowDir == "" {
		args.WorkflowDir = filepath.Dir(path)
	}

	var result ApplyResult
	if err := mut(raw, args, &result); err != nil {
		return ApplyResult{}, err
	}

	// No-op short-circuit: the mutator decided nothing needs to change.
	// We DON'T re-validate (no shape changes) and we DON'T write (avoid
	// spurious mtime bumps + content reformatting). Caller still gets
	// validatedOk=true so the audit line stays consistent.
	if result.NoOp {
		result.ValidatedOk = true
		return result, nil
	}

	// Re-encode to typed for validation. Use the marshalled bytes (not the
	// original on-disk bytes) so the validator sees the post-mutation shape.
	encoded, err := json.Marshal(raw)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("marshal workflow for validation: %w", err)
	}

	// CUE schema validation (TASK_02 / WF-SYNC-1): canonical pre-write
	// gate. The validator runs AFTER the in-memory mutation but BEFORE
	// the tmp+rename commit so a rejected payload leaves the file
	// bit-identical on disk.
	//
	// Bypass: args.NoSchemaCheck=true skips this gate AND appends to
	// `.browzer/audit/no-schema-check.log` so the operator-elected bypass
	// is auditable. Audit-write failure aborts the bypass — better to
	// fail loudly than silently lose the audit signal. The daemon path
	// will start honouring NoSchemaCheck in TASK_06; until then the
	// daemon ALWAYS validates the field UNLESS the
	// BROWZER_NO_SCHEMA_CHECK=1 env var is set (used by integration
	// tests that exercise daemon mechanics with non-conformant
	// fixtures).
	//
	// SA-02 / F-06 (2026-05-04): both bypass paths (the explicit
	// --no-schema-check flag AND the BROWZER_NO_SCHEMA_CHECK=1 env var)
	// MUST audit-log identically. The historical "env-var path is
	// silent" behaviour created an undocumented, unaudited bypass — any
	// CD pipeline or developer shell that set the env var would write
	// without a schema gate AND without a trail. The env-bypass path
	// now emits a `verb=env-bypass` audit line so operators can grep
	// `.browzer/audit/no-schema-check.log` and see it.
	envBypass := os.Getenv("BROWZER_NO_SCHEMA_CHECK") == "1"
	switch {
	case args.NoSchemaCheck:
		repoRoot := args.AuditRepoRoot
		if repoRoot == "" {
			repoRoot = schema.FindRepoRoot(filepath.Dir(path))
		}
		if auditErr := schema.RecordNoSchemaCheck(repoRoot, verb, path, encoded); auditErr != nil {
			return ApplyResult{}, fmt.Errorf("no-schema-check audit log: %w", auditErr)
		}
	case envBypass:
		repoRoot := args.AuditRepoRoot
		if repoRoot == "" {
			repoRoot = schema.FindRepoRoot(filepath.Dir(path))
		}
		// Audit verb is prefixed `env-bypass:` so operators can
		// distinguish env-var bypasses from explicit --no-schema-check
		// flag bypasses when grepping the log.
		auditVerb := "env-bypass:" + verb
		if auditErr := schema.RecordNoSchemaCheck(repoRoot, auditVerb, path, encoded); auditErr != nil {
			return ApplyResult{}, fmt.Errorf("no-schema-check env-bypass audit log: %w", auditErr)
		}
	default:
		validation := schema.ValidateWorkflow(encoded)
		if !validation.Valid {
			return ApplyResult{}, fmt.Errorf("schema validation failed:\n%s",
				schema.FormatViolations(validation.Violations))
		}
	}

	// Legacy structural validation (kept for backwards-compat with skill
	// bodies that grep for "validation error: <path>: <message>"). The
	// CUE pass above is the authoritative gate post-WF-SYNC-1; this pass
	// remains as defence-in-depth + a stable error format the legacy
	// rubric already understands.
	var typed Workflow
	if err := json.Unmarshal(encoded, &typed); err != nil {
		return ApplyResult{}, fmt.Errorf("re-parse workflow for validation: %w", err)
	}
	if errs := Validate(typed); len(errs) > 0 {
		return ApplyResult{}, fmt.Errorf("validation error: %s: %s", errs[0].Path, errs[0].Message)
	}
	result.ValidatedOk = true

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return ApplyResult{}, fmt.Errorf("marshal workflow: %w", err)
	}
	out = append(out, '\n')

	if awaitDurability {
		if err := atomicWriteFsync(path, out); err != nil {
			return result, err
		}
		result.Durable = true
		return result, nil
	}

	if err := AtomicWrite(path, out); err != nil {
		return result, err
	}
	return result, nil
}

// atomicWriteFsync writes data to path atomically AND fsyncs the file +
// parent directory before returning. Mirrors AtomicWrite's allocator-friendly
// unique-tmp-name approach.
//
// Steps: open tmp → write → f.Sync() (fsync data) → f.Close() → rename →
// open dir → dir.Sync() (fsync metadata) → dir.Close().
//
// Crash safety:
//  - Crash before tmp.Sync(): tmp file may be partially written; not visible.
//  - Crash between tmp.Sync() and rename: tmp file fully durable; not visible.
//  - Crash between rename and dir.Sync(): rename done in page cache, may roll
//    back on power loss. Fix: dir.Sync() AFTER rename.
//  - Crash after dir.Sync(): rename + contents both durable. Done.
func atomicWriteFsync(path string, data []byte) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	f, err := os.CreateTemp(dir, base+".*.tmp")
	if err != nil {
		return fmt.Errorf("atomic write open tmp: %w", err)
	}
	tmpPath := f.Name()

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic write data: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic write fsync: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic write close: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic write rename: %w", err)
	}

	df, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("atomic write open dir: %w", err)
	}
	if err := df.Sync(); err != nil {
		_ = df.Close()
		return fmt.Errorf("atomic write dir fsync: %w", err)
	}
	if err := df.Close(); err != nil {
		return fmt.Errorf("atomic write dir close: %w", err)
	}
	return nil
}

// --- Mutator implementations ------------------------------------------------
//
// Each mutator mirrors the corresponding cobra command's RMW logic. They
// operate on raw map[string]any so that exotic / forward-compat fields the
// typed schema doesn't know about survive round-trips.

func mutatorAppendStep(raw map[string]any, args MutatorArgs, out *ApplyResult) error {
	if len(args.Payload) == 0 {
		return fmt.Errorf("append-step: payload is required")
	}
	var stepMap map[string]any
	if err := json.Unmarshal(args.Payload, &stepMap); err != nil {
		return fmt.Errorf("parse step payload: %w", err)
	}

	stepsRaw := raw["steps"]
	stepsSlice, _ := stepsRaw.([]any)
	stepsSlice = append(stepsSlice, stepMap)
	raw["steps"] = stepsSlice

	recomputeCountersRaw(raw)

	if id, _ := stepMap["stepId"].(string); id != "" {
		out.StepID = id
	}
	return nil
}

func mutatorUpdateStep(raw map[string]any, args MutatorArgs, out *ApplyResult) error {
	if len(args.Args) < 1 || args.Args[0] == "" {
		return fmt.Errorf("update-step: stepId is required")
	}
	stepID := args.Args[0]
	out.StepID = stepID

	// Remaining args are field=value pairs.
	pairs := args.Args[1:]
	if len(pairs) == 0 {
		return fmt.Errorf("update-step: at least one field=value pair is required")
	}

	stepMap, _, err := findStepRaw(raw, stepID)
	if err != nil {
		return err
	}
	for _, pair := range pairs {
		idx := strings.IndexByte(pair, '=')
		if idx < 0 {
			return fmt.Errorf("update-step: invalid field=value pair %q", pair)
		}
		field := pair[:idx]
		value := pair[idx+1:]
		// F-SE-3 (2026-05-04): type-coerce well-known numeric/boolean
		// fields per the CUE schema. Without this, `update-step
		// elapsedMin=5.2` would write the string "5.2" where the schema
		// requires (float & >=0) | (int & >=0), and the post-mutation
		// CUE gate would reject the payload. Enum fields (status, name)
		// stay as strings — CUE enums are string disjunctions, so the
		// raw string flows through unchanged.
		stepMap[field] = coerceUpdateStepValue(field, value)
	}
	recomputeCountersRaw(raw)
	return nil
}

// updateStepCoerceFields lists the step-payload fields that are NOT
// strings in the CUE schema. Each entry is the (in-step) field name as
// the operator would type it on the CLI; the value is the JSON-native
// kind to coerce to. Enum-of-string fields (status, name, kind, etc.)
// are deliberately omitted — they round-trip as strings.
//
// Keep this list narrow: the coercion only fires when the operator
// passes one of these fields. Unknown fields fall through as strings,
// preserving the historic write-through behaviour for forward-compat
// fields the CUE schema doesn't yet name.
var updateStepCoerceFields = map[string]string{
	"elapsedMin":      "number",
	"executionDepth":  "number",
	"totalElapsedMin": "number",
	"totalSteps":      "number",
	"completedSteps":  "number",
	// Booleans known to flow through update-step today.
	"testExecutionDepthAuto": "bool",
	"isFinal":                "bool",
}

// coerceUpdateStepValue converts a CLI-supplied string `value` into the
// JSON-native type expected by the schema for the named field. Returns
// the original string when the field is not in the coerce list or when
// the cast fails (e.g. operator typed `elapsedMin=oops`) — letting the
// downstream CUE validator surface the type-mismatch with a real error
// path instead of silently masking it.
func coerceUpdateStepValue(field, value string) any {
	kind, ok := updateStepCoerceFields[field]
	if !ok {
		return value
	}
	var parsed any
	if jsonErr := json.Unmarshal([]byte(value), &parsed); jsonErr != nil {
		// Bad input — let the validator emit a typed error so the
		// operator sees the real path.
		return value
	}
	switch kind {
	case "number":
		switch parsed.(type) {
		case float64, int, int64:
			return parsed
		default:
			return value
		}
	case "bool":
		if _, isBool := parsed.(bool); isBool {
			return parsed
		}
		return value
	default:
		return parsed
	}
}

func mutatorCompleteStep(raw map[string]any, args MutatorArgs, out *ApplyResult) error {
	if len(args.Args) < 1 || args.Args[0] == "" {
		return fmt.Errorf("complete-step: stepId is required")
	}
	stepID := args.Args[0]
	out.StepID = stepID

	stepMap, _, err := findStepRaw(raw, stepID)
	if err != nil {
		return err
	}
	if strings.EqualFold(fmt.Sprintf("%v", stepMap["status"]), StatusCompleted) {
		// Idempotent: caller asked to complete a step already marked
		// COMPLETED. Skip the write entirely so the file's bytes-on-disk
		// stay bit-identical (avoids spurious git diffs / fsnotify
		// triggers / hash mismatches in downstream tooling).
		out.NoOp = true
		out.NoOpReason = "already_completed"
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	stepMap["status"] = StatusCompleted
	stepMap["completedAt"] = now
	stampElapsedMin(stepMap, now)
	// Workflow-level stamp on the final step (COMMIT or last-pending).
	wasFinal := stampWorkflowTotalElapsedIfFinal(raw, stepMap, now)
	if wasFinal {
		// TASK_05 hybrid-cache: clear .browzer/active-step on workflow
		// completion so ad-hoc claude runs that follow don't see a stale
		// stepId in their Langfuse traces.
		clearActiveStepCache(args.WorkflowDir)
	}
	recomputeCountersRaw(raw)
	return nil
}

func mutatorSetStatus(raw map[string]any, args MutatorArgs, out *ApplyResult) error {
	if len(args.Args) < 2 {
		return fmt.Errorf("set-status: requires <stepId> <status>")
	}
	stepID := args.Args[0]
	newStatus := args.Args[1]
	out.StepID = stepID

	stepMap, _, err := findStepRaw(raw, stepID)
	if err != nil {
		return err
	}
	current := fmt.Sprintf("%v", stepMap["status"])
	allowed, fromKnown := setStatusLegalTransitions[current]
	if !fromKnown {
		// Unknown current status: allow transition to any status legal from
		// PENDING. Mirrors the cobra command's behavior for forward-compat.
		allowed = setStatusLegalTransitions[StatusPending]
	}
	if !allowed[newStatus] {
		return fmt.Errorf("illegal status transition %s → %s", current, newStatus)
	}
	stepMap["status"] = newStatus
	now := time.Now().UTC().Format(time.RFC3339)
	// Auto-stamp startedAt on the first transition into RUNNING. Idempotent:
	// re-entry of an already-started step (e.g. post-staging-regression) MUST
	// preserve the original startedAt so elapsedMin reflects total wall-clock,
	// not just the latest re-entry window.
	if newStatus == StatusRunning && !stringFieldSet(stepMap, "startedAt") {
		stepMap["startedAt"] = now
	}
	if newStatus == StatusCompleted {
		stepMap["completedAt"] = now
		stampElapsedMin(stepMap, now)
		// Workflow-level stamp on the final step (COMMIT or last-pending).
		if stampWorkflowTotalElapsedIfFinal(raw, stepMap, now) {
			clearActiveStepCache(args.WorkflowDir)
		}
	}
	// F-10: when a step is aborted (STOPPED) and all remaining steps are also
	// terminal, the workflow is done — clear the active-step cache.
	if newStatus == StatusStopped && allStepsTerminal(raw) {
		clearActiveStepCache(args.WorkflowDir)
	}
	recomputeCountersRaw(raw)
	return nil
}

// stringFieldSet returns true when stepMap[field] is a non-empty string.
// Treats nil, missing, and "" identically — all of them mean "not yet stamped".
func stringFieldSet(stepMap map[string]any, field string) bool {
	v, ok := stepMap[field]
	if !ok || v == nil {
		return false
	}
	s, ok := v.(string)
	return ok && s != ""
}

// stampElapsedMin computes elapsedMin = (completedAt - startedAt) / 60 (in
// minutes, as a float64 to match the schema) and writes it onto stepMap. No-op
// when startedAt is missing, malformed, or after completedAt (returns silently
// rather than emitting bogus values).
//
// Tolerates a missing trailing 'Z' in startedAt for forward-compat with older
// payloads that used local-tz timestamps; tries RFC3339 first, then a
// best-effort RFC3339Nano parse.
func stampElapsedMin(stepMap map[string]any, completedAt string) {
	startedRaw, ok := stepMap["startedAt"]
	if !ok || startedRaw == nil {
		return
	}
	started, ok := startedRaw.(string)
	if !ok || started == "" {
		return
	}
	startedT, err := time.Parse(time.RFC3339, started)
	if err != nil {
		startedT, err = time.Parse(time.RFC3339Nano, started)
		if err != nil {
			return
		}
	}
	completedT, err := time.Parse(time.RFC3339, completedAt)
	if err != nil {
		completedT, err = time.Parse(time.RFC3339Nano, completedAt)
		if err != nil {
			return
		}
	}
	delta := completedT.Sub(startedT).Minutes()
	if delta < 0 {
		// Clock skew or out-of-order timestamps. Don't lie — leave the
		// existing field alone so the audit trail surfaces the anomaly.
		return
	}
	stepMap["elapsedMin"] = delta
}

// allStepsTerminal returns true when raw["steps"] is non-empty and every step
// has a status in {COMPLETED, SKIPPED, STOPPED}. Used by both
// stampWorkflowTotalElapsedIfFinal and mutatorSetStatus (F-10).
func allStepsTerminal(raw map[string]any) bool {
	stepsRaw, _ := raw["steps"].([]any)
	if len(stepsRaw) == 0 {
		return false
	}
	for _, s := range stepsRaw {
		sm, ok := s.(map[string]any)
		if !ok {
			return false
		}
		status := fmt.Sprintf("%v", sm["status"])
		switch status {
		case StatusCompleted, StatusSkipped, StatusStopped:
			// terminal — ok
		default:
			return false
		}
	}
	return true
}

// stampWorkflowTotalElapsedIfFinal stamps workflow-level totalElapsedMin and
// completedAt when the just-completed step is the final step of the workflow.
//
// "Final" means either:
//   - The completed step's name is "COMMIT", OR
//   - All steps have status in {COMPLETED, SKIPPED, STOPPED} (no PENDING /
//     RUNNING / AWAITING_REVIEW remaining after this step's status flip).
//
// Idempotent: if raw["totalElapsedMin"] is already a positive number the
// function returns immediately without re-stamping.
//
// Returns silently on any parse error — same defensive pattern as stampElapsedMin.
//
// Returns true when the just-completed step IS final (regardless of whether
// stamping actually occurred — idempotent re-entries return true so callers
// can run final-step side effects like clearing the .browzer/active-step
// cache exactly once per workflow).
func stampWorkflowTotalElapsedIfFinal(raw map[string]any, stepMap map[string]any, now string) bool {
	// Idempotency guard for the stamp itself. Final-step side effects
	// (cache clear) still fire on re-entry — they're cheap and idempotent.
	alreadyStamped := false
	switch v := raw["totalElapsedMin"].(type) {
	case float64:
		alreadyStamped = v > 0
	case int:
		alreadyStamped = v > 0
	}

	// Check whether this is the final step.
	isFinal := false
	if name, _ := stepMap["name"].(string); strings.EqualFold(name, StepCommit) {
		isFinal = true
	}
	if !isFinal {
		isFinal = allStepsTerminal(raw)
	}
	if !isFinal {
		return false
	}
	if alreadyStamped {
		// Final, but the stamp already happened on a previous transition.
		// Return true so callers run idempotent final-step side effects.
		return true
	}

	// Compute elapsed from workflow startedAt.
	startedRaw, ok := raw["startedAt"]
	if !ok || startedRaw == nil {
		return true
	}
	started, ok := startedRaw.(string)
	if !ok || started == "" {
		return true
	}
	startedT, err := time.Parse(time.RFC3339, started)
	if err != nil {
		startedT, err = time.Parse(time.RFC3339Nano, started)
		if err != nil {
			return true
		}
	}
	completedT, err := time.Parse(time.RFC3339, now)
	if err != nil {
		completedT, err = time.Parse(time.RFC3339Nano, now)
		if err != nil {
			return true
		}
	}
	delta := completedT.Sub(startedT).Minutes()
	if delta < 0 {
		return true
	}
	raw["totalElapsedMin"] = math.Round(delta*100) / 100
	raw["completedAt"] = now
	return true
}

// setStatusLegalTransitions is the SINGLE source of truth for the
// step-status state machine. The historic copy in
// `internal/commands/workflow_set_status.go` was deleted when the cobra
// RunE moved to dispatchToDaemonOrFallback (both paths now route through
// `wf.ApplyAndPersist`). Kept in package `workflow` so the daemon
// goroutine path doesn't depend on the commands package (would create
// an import cycle: commands → workflow → commands).
//
// F-SE-2 (2026-05-04): comment previously read "duplicates the table in
// workflow_set_status.go", which was true at one point but is now false.
// If you intend to add a second copy elsewhere, refactor first.
var setStatusLegalTransitions = map[StepStatus]map[StepStatus]bool{
	StatusPending: {
		StatusRunning:        true,
		StatusAwaitingReview: true,
		StatusCompleted:      true,
		StatusSkipped:        true,
		StatusStopped:        true,
	},
	StatusRunning: {
		StatusCompleted:      true,
		StatusAwaitingReview: true,
		StatusStopped:        true,
	},
	StatusAwaitingReview: {
		StatusPending:   true,
		StatusRunning:   true,
		StatusCompleted: true,
		StatusSkipped:   true,
		StatusStopped:   true,
	},
	StatusPausedPendingOp: {
		StatusRunning:        true,
		StatusCompleted:      true,
		StatusSkipped:        true,
		StatusStopped:        true,
		StatusAwaitingReview: true,
	},
	StatusCompleted: {},
	StatusSkipped:   {},
	StatusStopped:   {},
}

func mutatorSetConfig(raw map[string]any, args MutatorArgs, _ *ApplyResult) error {
	if len(args.Args) < 2 {
		return fmt.Errorf("set-config: requires <key> <value>")
	}
	key := args.Args[0]
	rawValue := args.Args[1]

	if legal, ok := setConfigLegalValues[key]; ok {
		if !legal[rawValue] {
			return fmt.Errorf("illegal value %q for config key %q", rawValue, key)
		}
	}

	configRaw, ok := raw["config"]
	if !ok {
		configRaw = map[string]any{}
	}
	configMap, ok := configRaw.(map[string]any)
	if !ok {
		configMap = map[string]any{}
	}

	var parsed any
	if jsonErr := json.Unmarshal([]byte(rawValue), &parsed); jsonErr != nil {
		parsed = rawValue
	}
	configMap[key] = parsed
	configMap["setAt"] = time.Now().UTC().Format(time.RFC3339)
	raw["config"] = configMap
	raw["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
	return nil
}

var setConfigLegalValues = map[string]map[string]bool{
	"mode": {
		"autonomous": true,
		"review":     true,
	},
}

func mutatorAppendReviewHistory(raw map[string]any, args MutatorArgs, out *ApplyResult) error {
	if len(args.Args) < 1 || args.Args[0] == "" {
		return fmt.Errorf("append-review-history: stepId is required")
	}
	stepID := args.Args[0]
	out.StepID = stepID

	if len(args.Payload) == 0 {
		return fmt.Errorf("append-review-history: payload is required")
	}
	var entry map[string]any
	if err := json.Unmarshal(args.Payload, &entry); err != nil {
		return fmt.Errorf("parse review entry: %w", err)
	}

	// Accept legacy field names (at, timestamp, action, decision, note, comment)
	// and translate to CUE-canonical names (decidedAt, operatorAction, operatorNote)
	// per #ReviewExchange in packages/cli/schemas/workflow-v1.cue.
	if _, hasDecidedAt := entry["decidedAt"]; !hasDecidedAt {
		if at, ok := entry["at"].(string); ok && at != "" {
			entry["decidedAt"] = at
			delete(entry, "at")
		} else if ts, ok := entry["timestamp"].(string); ok && ts != "" {
			entry["decidedAt"] = ts
			delete(entry, "timestamp")
		}
	} else {
		delete(entry, "at")
		delete(entry, "timestamp")
	}

	if _, hasOpAction := entry["operatorAction"]; !hasOpAction {
		if act, ok := entry["action"]; ok {
			entry["operatorAction"] = act
			delete(entry, "action")
		} else if dec, ok := entry["decision"]; ok {
			entry["operatorAction"] = dec
			delete(entry, "decision")
		}
	} else {
		delete(entry, "action")
		delete(entry, "decision")
	}

	if _, hasOpNote := entry["operatorNote"]; !hasOpNote {
		if n, ok := entry["note"]; ok {
			entry["operatorNote"] = n
			delete(entry, "note")
		} else if c, ok := entry["comment"]; ok {
			entry["operatorNote"] = c
			delete(entry, "comment")
		}
	} else {
		delete(entry, "note")
		delete(entry, "comment")
	}

	// Translate enum values: legacy gerund forms → CUE-canonical infinitive forms.
	if actVal, ok := entry["operatorAction"].(string); ok {
		if mapped, hit := appendReviewHistoryLegacyActions[actVal]; hit {
			entry["operatorAction"] = mapped
		}
	}

	// Validate translated entry against #ReviewExchange contract.
	decidedAtStr, _ := entry["decidedAt"].(string)
	if decidedAtStr == "" {
		return fmt.Errorf("review entry missing required field 'decidedAt' (or legacy 'at'/'timestamp')")
	}
	actionVal, hasAction := entry["operatorAction"]
	if !hasAction || actionVal == nil {
		return fmt.Errorf("review entry missing required field 'operatorAction' (or legacy 'action'/'decision')")
	}
	actionStr, _ := actionVal.(string)
	if actionStr != "" && !appendReviewHistoryLegalActions[actionStr] {
		return fmt.Errorf("invalid review action %q: must be one of approve|adjust|skip|stop", actionStr)
	}

	stepMap, _, err := findStepRaw(raw, stepID)
	if err != nil {
		return err
	}
	rh, _ := stepMap["reviewHistory"].([]any)
	// Default 'round' (CUE: int & >=1) — auto-increment based on existing history.
	if _, hasRound := entry["round"]; !hasRound {
		entry["round"] = len(rh) + 1
	}
	// Default 'proposal' (CUE: string) — empty string is permitted by schema.
	if _, hasProposal := entry["proposal"]; !hasProposal {
		entry["proposal"] = ""
	}
	// Default 'operatorNote' (CUE: *"" | string) — empty string when absent.
	if _, hasOpNote := entry["operatorNote"]; !hasOpNote {
		entry["operatorNote"] = ""
	}
	rh = append(rh, entry)
	stepMap["reviewHistory"] = rh
	raw["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
	return nil
}

// appendReviewHistoryLegalActions enumerates CUE-canonical operatorAction values
// per #ReviewExchange in packages/cli/schemas/workflow-v1.cue.
var appendReviewHistoryLegalActions = map[string]bool{
	"approve": true,
	"adjust":  true,
	"skip":    true,
	"stop":    true,
}

// appendReviewHistoryLegacyActions maps legacy gerund-form action values
// (used by older callers) to the CUE-canonical infinitive form.
var appendReviewHistoryLegacyActions = map[string]string{
	"approved": "approve",
	"edited":   "adjust",
	"skipped":  "skip",
	"stopped":  "stop",
}

func mutatorSetCurrentStep(raw map[string]any, args MutatorArgs, out *ApplyResult) error {
	if len(args.Args) < 1 || args.Args[0] == "" {
		return fmt.Errorf("set-current-step: stepId is required")
	}
	stepID := args.Args[0]
	out.StepID = stepID

	stepMap, _, err := findStepRaw(raw, stepID)
	if err != nil {
		return err
	}
	raw["currentStepId"] = stepID
	nextStep, _ := stepMap["nextStep"].(string)
	raw["nextStepId"] = nextStep
	raw["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
	// TASK_05 hybrid-cache: write derived cache so langfuse_hook.py can
	// resolve the active stepId without parsing workflow.json on every
	// tool call. Fire-and-forget; cache failures must not abort the
	// mutation. workflow.json.currentStepId remains the canonical SSOT.
	if args.WorkflowDir != "" {
		writeActiveStepCache(args.WorkflowDir, stepID)
	}
	return nil
}

// writeActiveStepCache atomically writes stepID to
// <workflowDir>/.browzer/active-step. The directory is created if needed.
// Errors are logged to stderr but never returned — this is a best-effort
// side-channel cache; the workflow mutation succeeds regardless.
//
// TASK_05 hybrid-cache: workflow.json.currentStepId is the canonical SSOT;
// this file is a regenerable derived projection consumed by
// .claude/hooks/langfuse_hook.py to attribute traces to the active step.
func writeActiveStepCache(workflowDir, stepID string) {
	cacheDir := filepath.Join(workflowDir, ".browzer")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "warn: create .browzer dir for active-step cache: %v\n", err)
		return
	}
	cacheFile := filepath.Join(cacheDir, "active-step")
	if err := AtomicWrite(cacheFile, []byte(stepID)); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "warn: write .browzer/active-step cache: %v\n", err)
	}
}

// clearActiveStepCache deletes <workflowDir>/.browzer/active-step. Called
// when the final workflow step transitions to a terminal status. Missing
// file is not an error — mirrors the fire-and-forget contract of
// writeActiveStepCache.
func clearActiveStepCache(workflowDir string) {
	if workflowDir == "" {
		return
	}
	cacheFile := filepath.Join(workflowDir, ".browzer", "active-step")
	if err := os.Remove(cacheFile); err != nil && !os.IsNotExist(err) {
		_, _ = fmt.Fprintf(os.Stderr, "warn: remove .browzer/active-step cache: %v\n", err)
	}
}

func mutatorPatch(raw map[string]any, args MutatorArgs, _ *ApplyResult) error {
	if args.JQExpr == "" {
		return fmt.Errorf("patch: --jq is required")
	}
	result, err := ApplyJQWithVars(raw, args.JQExpr, args.JQVars)
	if err != nil {
		return fmt.Errorf("jq error: %w", err)
	}
	resultMap, ok := result.(map[string]any)
	if !ok {
		// gojq sometimes returns map[interface{}]interface{} — round-trip
		// through JSON to normalise.
		b, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return fmt.Errorf("jq result is not a JSON object: %T", result)
		}
		if err := json.Unmarshal(b, &resultMap); err != nil {
			return fmt.Errorf("jq result is not a JSON object: %T", result)
		}
	}
	resultMap["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
	// Replace the entire raw map in-place: clear keys then copy.
	clear(raw)
	maps.Copy(raw, resultMap)
	return nil
}

// findStepRaw locates a step by stepId in raw["steps"]. Returns the step map
// + index, or an error mirroring findStepInRaw in workflow_mutator_helpers.go.
func findStepRaw(raw map[string]any, stepID string) (map[string]any, int, error) {
	stepsRaw, ok := raw["steps"]
	if !ok {
		return nil, -1, fmt.Errorf("step %q not found: workflow has no steps", stepID)
	}
	stepsSlice, ok := stepsRaw.([]any)
	if !ok {
		return nil, -1, fmt.Errorf("steps field is not an array")
	}
	for i, s := range stepsSlice {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if sm["stepId"] == stepID {
			return sm, i, nil
		}
	}
	return nil, -1, fmt.Errorf("step %q not found in workflow", stepID)
}

// --- New mutators (Phase 3) ---------------------------------------------------

// mutatorReapplyAdditionalContext applies reviewer.additionalContext.changes[]
// to task.scope. Each change entry has a `kind` field ("corrected", "added", or
// "dropped") plus path/from/to fields. The operation is idempotent: re-running
// on a scope that already reflects the changes is a NoOp.
//
// Args: args.Args[0] = stepId.
// Closes Phase 2 item #8 from the plan.
func mutatorReapplyAdditionalContext(raw map[string]any, args MutatorArgs, out *ApplyResult) error {
	if len(args.Args) < 1 || args.Args[0] == "" {
		return fmt.Errorf("reapply-additional-context: stepId is required")
	}
	stepID := args.Args[0]
	out.StepID = stepID

	stepMap, _, err := findStepRaw(raw, stepID)
	if err != nil {
		return err
	}

	// Navigate: step.task.reviewer.additionalContext.changes[]
	taskRaw, ok := stepMap["task"]
	if !ok {
		out.NoOp = true
		out.NoOpReason = "no_task_field"
		return nil
	}
	taskMap, ok := taskRaw.(map[string]any)
	if !ok {
		out.NoOp = true
		out.NoOpReason = "task_not_object"
		return nil
	}

	reviewerRaw, ok := taskMap["reviewer"]
	if !ok {
		out.NoOp = true
		out.NoOpReason = "no_reviewer_field"
		return nil
	}
	reviewerMap, ok := reviewerRaw.(map[string]any)
	if !ok {
		out.NoOp = true
		out.NoOpReason = "reviewer_not_object"
		return nil
	}

	acRaw, ok := reviewerMap["additionalContext"]
	if !ok {
		out.NoOp = true
		out.NoOpReason = "no_additionalContext_field"
		return nil
	}
	acMap, ok := acRaw.(map[string]any)
	if !ok {
		out.NoOp = true
		out.NoOpReason = "additionalContext_not_object"
		return nil
	}

	changesRaw, ok := acMap["changes"]
	if !ok {
		out.NoOp = true
		out.NoOpReason = "no_changes_field"
		return nil
	}
	changes, ok := changesRaw.([]any)
	if !ok || len(changes) == 0 {
		out.NoOp = true
		out.NoOpReason = "empty_changes"
		return nil
	}

	// Read current scope.
	scopeSlice, _ := taskMap["scope"].([]any)

	// Apply changes. Track whether anything actually changed.
	changed := false
	for _, chRaw := range changes {
		ch, ok := chRaw.(map[string]any)
		if !ok {
			continue
		}
		kind, _ := ch["kind"].(string)
		switch kind {
		case "corrected":
			from, _ := ch["from"].(string)
			to, _ := ch["to"].(string)
			if from == "" || to == "" {
				continue
			}
			for i, s := range scopeSlice {
				if s == from {
					scopeSlice[i] = to
					changed = true
					break
				}
			}
		case "added":
			path, _ := ch["path"].(string)
			if path == "" {
				path, _ = ch["to"].(string)
			}
			if path == "" {
				continue
			}
			// Idempotent: only add if not already present.
			found := false
			for _, s := range scopeSlice {
				if s == path {
					found = true
					break
				}
			}
			if !found {
				scopeSlice = append(scopeSlice, path)
				changed = true
			}
		case "dropped":
			path, _ := ch["path"].(string)
			if path == "" {
				path, _ = ch["from"].(string)
			}
			if path == "" {
				continue
			}
			before := len(scopeSlice)
			// F-05 (2026-05-04): allocate a fresh slice rather than
			// reusing scopeSlice's backing array via `scopeSlice[:0]`.
			// In-place filtering aliases the original backing array,
			// which is correct only because `len(survivors) <= len(original)`
			// and elements are pure values — fragile if elements ever
			// become pointers/slices (would cause double-write). The
			// extra allocation is negligible vs. the JSON parse path.
			filtered := make([]any, 0, len(scopeSlice))
			for _, s := range scopeSlice {
				if s != path {
					filtered = append(filtered, s)
				}
			}
			scopeSlice = filtered
			if len(scopeSlice) != before {
				changed = true
			}
		}
	}

	if !changed {
		out.NoOp = true
		out.NoOpReason = "already_applied"
		return nil
	}

	taskMap["scope"] = scopeSlice
	raw["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
	return nil
}

// mutatorAuditModelOverride records a model override onto a TASK step's
// task.execution.modelOverride field.
//
// Args: args.Args[0]=stepId, args.Args[1]=fromModel, args.Args[2]=toModel,
//
//	args.Args[3]=reason.
//
// Closes Phase 3 item #9 from the plan.
func mutatorAuditModelOverride(raw map[string]any, args MutatorArgs, out *ApplyResult) error {
	if len(args.Args) < 4 {
		return fmt.Errorf("audit-model-override: requires <stepId> <fromModel> <toModel> <reason>")
	}
	stepID := args.Args[0]
	fromModel := args.Args[1]
	toModel := args.Args[2]
	reason := args.Args[3]
	out.StepID = stepID

	stepMap, _, err := findStepRaw(raw, stepID)
	if err != nil {
		return err
	}

	// Navigate / create: step.task.execution
	taskRaw, ok := stepMap["task"]
	if !ok {
		taskRaw = map[string]any{}
		stepMap["task"] = taskRaw
	}
	taskMap, ok := taskRaw.(map[string]any)
	if !ok {
		return fmt.Errorf("audit-model-override: step %q task field is not an object", stepID)
	}

	executionRaw, ok := taskMap["execution"]
	if !ok {
		executionRaw = map[string]any{}
		taskMap["execution"] = executionRaw
	}
	executionMap, ok := executionRaw.(map[string]any)
	if !ok {
		return fmt.Errorf("audit-model-override: step %q task.execution is not an object", stepID)
	}

	executionMap["modelOverride"] = map[string]any{
		"from":   fromModel,
		"to":     toModel,
		"reason": reason,
		"at":     time.Now().UTC().Format(time.RFC3339),
	}
	raw["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
	return nil
}

// mutatorTruncationAudit appends a truncation-suspected warning to a step's
// warnings[] array. The payload (stdin) is a JSON object containing
// filesModified, filesCreated, filesDeleted arrays and lastCheckpoint.
//
// Args: args.Args[0]=stepId, args.Args[1]=lastCheckpoint (optional; may also
// come from payload). The payload JSON is read from args.Payload.
//
// Closes Phase 3 item #11 from the plan.
func mutatorTruncationAudit(raw map[string]any, args MutatorArgs, out *ApplyResult) error {
	if len(args.Args) < 1 || args.Args[0] == "" {
		return fmt.Errorf("truncation-audit: stepId is required")
	}
	stepID := args.Args[0]
	out.StepID = stepID

	// lastCheckpoint from arg or payload.
	lastCheckpoint := ""
	if len(args.Args) >= 2 {
		lastCheckpoint = args.Args[1]
	}

	// Decode payload.
	var payload struct {
		FilesModified []string `json:"filesModified"`
		FilesCreated  []string `json:"filesCreated"`
		FilesDeleted  []string `json:"filesDeleted"`
		// Also accept lastCheckpoint from payload body.
		LastCheckpoint string `json:"lastCheckpoint"`
	}
	if len(args.Payload) > 0 {
		if err := json.Unmarshal(args.Payload, &payload); err != nil {
			return fmt.Errorf("truncation-audit: parse payload: %w", err)
		}
		if lastCheckpoint == "" && payload.LastCheckpoint != "" {
			lastCheckpoint = payload.LastCheckpoint
		}
	}

	stepMap, _, err := findStepRaw(raw, stepID)
	if err != nil {
		return err
	}

	// Convert slice fields to []any for storage.
	toAnySlice := func(ss []string) []any {
		out := make([]any, len(ss))
		for i, s := range ss {
			out[i] = s
		}
		return out
	}

	warning := map[string]any{
		"at":             time.Now().UTC().Format(time.RFC3339),
		"kind":           "truncation-suspected",
		"filesModified":  toAnySlice(payload.FilesModified),
		"filesCreated":   toAnySlice(payload.FilesCreated),
		"filesDeleted":   toAnySlice(payload.FilesDeleted),
		"lastCheckpoint": lastCheckpoint,
		"remediation":    "re-dispatch with subagent-preamble §4.5 emphasis",
	}

	warnings, _ := stepMap["warnings"].([]any)
	warnings = append(warnings, warning)
	stepMap["warnings"] = warnings
	raw["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
	return nil
}

// mutatorAppendDispatch appends a #DispatchRecord to step.dispatches[].
//
// Args: args.Args[0] = stepId.
// Payload: JSON-encoded map representing the #DispatchRecord (produced by the
// cobra command after computing sha256 + spool path). The mutator does NOT
// pre-validate the record shape — schema validation (TASK_02) runs after the
// mutation in ApplyAndPersist and rejects any missing required fields.
func mutatorAppendDispatch(raw map[string]any, args MutatorArgs, out *ApplyResult) error {
	if len(args.Args) < 1 || args.Args[0] == "" {
		return fmt.Errorf("append-dispatch: stepId is required")
	}
	stepID := args.Args[0]
	out.StepID = stepID

	if len(args.Payload) == 0 {
		return fmt.Errorf("append-dispatch: payload is required")
	}
	var record map[string]any
	if err := json.Unmarshal(args.Payload, &record); err != nil {
		return fmt.Errorf("append-dispatch: parse dispatch record: %w", err)
	}

	stepMap, _, err := findStepRaw(raw, stepID)
	if err != nil {
		return err
	}

	dispatches, _ := stepMap["dispatches"].([]any)
	dispatches = append(dispatches, record)
	stepMap["dispatches"] = dispatches
	raw["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
	return nil
}

// recomputeCountersRaw mirrors recomputeCounters in workflow_mutator_helpers.go.
// Duplicated here to keep workflow package free of the commands import (the
// commands package uses workflow, not the other way round).
func recomputeCountersRaw(raw map[string]any) {
	stepsRaw := raw["steps"]
	stepsSlice, _ := stepsRaw.([]any)
	total := len(stepsSlice)
	completed := 0
	for _, s := range stepsSlice {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if sm["status"] == StatusCompleted {
			completed++
		}
	}
	raw["totalSteps"] = total
	raw["completedSteps"] = completed
	raw["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
}
