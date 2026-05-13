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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/browzeremb/browzer-cli/internal/schema"
)

// ApplyResult carries the output of one ApplyAndPersist call.
type ApplyResult struct {
	// StepID is the workflow step affected by the mutation. May be empty
	// for verbs that target the workflow document itself (e.g. backfill-elapsed).
	StepID string
	// ValidatedOk is true iff Validate(typed) returned no errors.
	ValidatedOk bool
	// Durable is true iff awaitDurability=true was honored end-to-end (file
	// fsync + parent dir fsync both succeeded). For awaitDurability=false the
	// field is always false even when the kernel ends up flushing the page.
	Durable bool
	// NoOp is true when the mutator decided no change was needed (e.g.
	// backfill-elapsed when all elapsed values are already set). When true ApplyAndPersist
	// SKIPS the marshal+write+fsync entirely so the file's bytes-on-disk
	// remain bit-identical and downstream tooling (git, fsnotify, parsers
	// that compare hashes) doesn't see a spurious touch.
	NoOp bool
	// NoOpReason is set together with NoOp=true to give the audit line a
	// human-readable explanation. Empty when NoOp=false.
	NoOpReason string

	// ExplorerProjected is true when the append-step / append-steps verb
	// auto-projected at least one TASK step's `task.explorer` payload from
	// the rich (agent-friendly) shape to the lean (CUE-canonical) shape
	// before validation. Surfaced in the audit line as
	// `explorerProjected=true` so the operator + judge can detect drift
	// between the subagent prompt and the schema (RETRO PR 6 §2.2). Set
	// to false unless the projection actually changed the payload.
	ExplorerProjected bool
}

// Mutator is the in-place transform applied to the raw workflow map under
// the caller's lock. The Mutator MUST set out.StepID when the verb is
// step-scoped so the caller's audit line can carry it.
//
// The payload is the raw JSON body the caller passed (CLI: --payload file
// or stdin; daemon: WorkflowMutateParams.Payload). Verbs that do not need a
// payload ignore it.
//
// args carries verb-specific positional arguments. Keeping these typed-as-args
// instead of leaking cobra/flag types keeps the Mutator surface portable.
type Mutator func(raw map[string]any, args MutatorArgs, out *ApplyResult) error

// MutatorArgs carries the per-call inputs that are not part of the workflow
// document itself: positional args and the JSON payload bytes.
type MutatorArgs struct {
	// Args are positional arguments after the verb.
	Args []string
	// Payload is the raw payload bytes. Used by append-step and append-steps.
	// Empty for the other verbs.
	Payload []byte
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

	// NoBackup skips the rotateWorkflowBackups call before the atomic write.
	// Intended for the autosave hook path (BROWZER_LLM=1 / --no-backup) where
	// a rapid series of saves would fill the .bak slots with near-identical
	// snapshots. Default false (rotate up to workflowBackupCount copies).
	NoBackup bool
}

// Mutators is the verb registry. Callers are: (1) orchestrator-driven
// `browzer workflow <verb>` cobra subcommands for the three surviving
// append/backfill mutators, and (2) the daemon JSON-RPC dispatcher.
// Skills under packages/skills do NOT call mutator verbs directly —
// they Write markdown files and the orchestrator drives save-step.
var Mutators = map[string]Mutator{
	"append-step":      mutatorAppendStep,
	"append-steps":     mutatorAppendSteps,
	"backfill-elapsed": mutatorBackfillElapsed,
}

// ErrUnknownVerb is returned by ApplyAndPersist when verb is not in Mutators.
var ErrUnknownVerb = errors.New("workflow: unknown verb")

// DryRunResult is the structured response of ApplyDryRun. Marshalled
// to stdout by `browzer workflow patch --dry-run` so the operator (or
// LLM) can inspect what WOULD change without touching the file.
//
// Field semantics:
//
//   - Ok: true iff every mutation in the pipeline succeeded AND the
//     post-mutation document passed both CUE validation and the
//     legacy structural validator.
//   - Errors: human-readable strings; each entry maps to one of (a)
//     mutator failure, (b) CUE schema violation, (c) legacy structural
//     violation. Empty when Ok=true.
//   - BeforeHash / AfterHash: sha256 of the pre/post document bytes
//     (post = the indented bytes that would have been written).
//     Equal hashes signal a no-op mutation.
//   - DiffPaths: the JSON-Pointer-ish dotted paths whose values
//     differ between before and after (top-level diff plus a
//     bounded recursive walk for nested map/array changes).
//   - StepID: optional — the step the mutator targeted, for the
//     audit-line correlation downstream.
type DryRunResult struct {
	Ok         bool     `json:"ok"`
	Errors     []string `json:"errors"`
	BeforeHash string   `json:"beforeHash"`
	AfterHash  string   `json:"afterHash"`
	DiffPaths  []string `json:"diffPaths"`
	StepID     string   `json:"stepId,omitempty"`
}

// runMutatorInMemory loads the workflow at path, applies verb's mutator with
// args, and returns the mutated raw map plus the indented JSON-encoded bytes.
// It does NOT write to disk and does NOT acquire any lock — the caller owns
// both concerns.
//
// Used as the shared preamble by ApplyDryRun and DryRunViolations so both
// functions share identical load/apply/encode logic without duplication.
func runMutatorInMemory(path, verb string, args MutatorArgs) (raw map[string]any, encoded []byte, err error) {
	mut, ok := Mutators[verb]
	if !ok {
		return nil, nil, fmt.Errorf("%w: %q", ErrUnknownVerb, verb)
	}
	rawBytes, rdErr := os.ReadFile(path)
	if rdErr != nil {
		return nil, nil, fmt.Errorf("read workflow: %w", rdErr)
	}
	if unmErr := json.Unmarshal(rawBytes, &raw); unmErr != nil {
		return nil, nil, fmt.Errorf("parse workflow map: %w", unmErr)
	}
	if raw == nil {
		raw = map[string]any{}
	}
	var result ApplyResult
	if mErr := mut(raw, args, &result); mErr != nil {
		return nil, nil, mErr
	}
	encoded, err = json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("marshal workflow: %w", err)
	}
	encoded = append(encoded, '\n')
	return raw, encoded, nil
}

// ApplyDryRun runs the mutate-and-validate pipeline WITHOUT writing
// the result back to disk. Designed for `--dry-run` callers: the
// operator (or LLM) gets a structured preview of the validation
// outcome and the field paths that would change.
//
// Pipeline (no lock — caller decides whether to acquire):
//  1. Read the file from disk.
//  2. Run the verb's Mutator on the raw map.
//  3. Marshal the mutated map back to JSON.
//  4. Run schema.ValidateWorkflow + the legacy structural validator
//     against the marshalled bytes. Both errors are captured into
//     DryRunResult.Errors; the function NEVER returns a Go error
//     for validation failures (so the caller can serialise the
//     result to JSON cleanly).
//  5. Compute (beforeHash, afterHash, diffPaths) for the response.
//
// File contents are not touched.
//
// `args` is the same MutatorArgs passed to ApplyAndPersist. The
// `verb` MUST be in `Mutators` — unknown verbs return an error.
func ApplyDryRun(path, verb string, args MutatorArgs) (DryRunResult, error) {
	if _, ok := Mutators[verb]; !ok {
		return DryRunResult{}, fmt.Errorf("%w: %q", ErrUnknownVerb, verb)
	}
	beforeBytes, err := os.ReadFile(path)
	if err != nil {
		return DryRunResult{}, fmt.Errorf("read workflow: %w", err)
	}
	beforeHash := sha256Hex(beforeBytes)

	// Snapshot the pre-mutation map for diff comparison.
	var beforeSnap map[string]any
	if err := json.Unmarshal(beforeBytes, &beforeSnap); err != nil {
		return DryRunResult{}, fmt.Errorf("snapshot workflow: %w", err)
	}

	raw, encoded, mErr := runMutatorInMemory(path, verb, args)
	if mErr != nil {
		return DryRunResult{
			Ok:         false,
			Errors:     []string{mErr.Error()},
			BeforeHash: beforeHash,
		}, nil
	}

	afterHash := sha256Hex(encoded)
	out := DryRunResult{
		BeforeHash: beforeHash,
		AfterHash:  afterHash,
		DiffPaths:  computeDiffPaths(beforeSnap, raw),
	}

	// CUE schema validation. Failures are non-fatal in the dry-run
	// envelope: we report them and return Ok=false.
	validation := schema.ValidateWorkflow(encoded)
	if !validation.Valid {
		out.Errors = append(out.Errors, schema.FormatViolations(validation.Violations))
	}

	// Legacy structural validation (Validate(typed)).
	var typed Workflow
	if err := json.Unmarshal(encoded, &typed); err != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("re-parse workflow: %v", err))
	} else if errs := Validate(typed); len(errs) > 0 {
		out.Errors = append(out.Errors,
			fmt.Sprintf("validation error: %s: %s", errs[0].Path, errs[0].Message))
	}

	out.Ok = len(out.Errors) == 0
	return out, nil
}

// DryRunViolations runs the same mutate-and-validate pipeline as ApplyDryRun
// but returns the raw schema.ValidationResult instead of pre-formatted error
// strings. This lets callers (e.g. `save-step --hint-fixes`) access the
// structured Violation slice with AllowedValues / AllowedFields populated,
// which is required to render worked examples.
//
// Returns (nil, err) when the mutator fails — the caller can inspect the error
// directly (previously this swallowed the error and returned (nil, nil)).
func DryRunViolations(path, verb string, args MutatorArgs) (*schema.ValidationResult, error) {
	if _, ok := Mutators[verb]; !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownVerb, verb)
	}
	_, encoded, mErr := runMutatorInMemory(path, verb, args)
	if mErr != nil {
		return nil, mErr // propagate — previously swallowed with (nil, nil)
	}
	res := schema.ValidateWorkflow(encoded)
	return &res, nil
}

// sha256Hex returns the hex-encoded SHA-256 digest of b. Used for
// dry-run before/after hashes — comparing the hashes is the cheapest
// "did anything change" check the operator can run.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// computeDiffPaths returns the dotted paths whose values differ
// between `before` and `after`. Recursion is bounded:
//
//   - Top-level keys: enumerated in both maps; keys present in only
//     one side are reported.
//   - Nested maps: recurse with the path extended.
//   - Nested arrays: a length mismatch is reported as `<path>.length`;
//     element-wise diffs use `<path>[i]` for changed indices.
//   - Scalars (string / number / bool / null): byte-compare via
//     json.Marshal — covers the nil-vs-"" / 1-vs-1.0 edge cases.
//
// Output is alpha-sorted to keep stdout-byte-stable across runs.
// The function caps recursion at 12 levels (defensive — workflow
// schemas don't go that deep).
func computeDiffPaths(before, after map[string]any) []string {
	paths := map[string]struct{}{}
	var walk func(string, any, any, int)
	walk = func(prefix string, a, b any, depth int) {
		if depth > 12 {
			paths[prefix] = struct{}{}
			return
		}
		am, aok := a.(map[string]any)
		bm, bok := b.(map[string]any)
		if aok && bok {
			seen := map[string]bool{}
			for k := range am {
				seen[k] = true
				next := joinPath(prefix, k)
				if _, hasB := bm[k]; !hasB {
					paths[next] = struct{}{}
					continue
				}
				walk(next, am[k], bm[k], depth+1)
			}
			for k := range bm {
				if seen[k] {
					continue
				}
				paths[joinPath(prefix, k)] = struct{}{}
			}
			return
		}
		// Slice diff.
		as, aok := a.([]any)
		bs, bok := b.([]any)
		if aok && bok {
			if len(as) != len(bs) {
				paths[prefix+".length"] = struct{}{}
				// Continue with min-length compare so element changes
				// upstream of the length boundary still surface.
			}
			n := len(as)
			if len(bs) < n {
				n = len(bs)
			}
			for i := 0; i < n; i++ {
				walk(fmt.Sprintf("%s[%d]", prefix, i), as[i], bs[i], depth+1)
			}
			return
		}
		// Scalar / mixed-kind compare. Marshal to JSON to side-step
		// nil-vs-typed-nil and float-vs-int gotchas.
		ab, _ := json.Marshal(a)
		bb, _ := json.Marshal(b)
		if !bytesEqual(ab, bb) {
			if prefix == "" {
				prefix = "<root>"
			}
			paths[prefix] = struct{}{}
		}
	}
	walk("", before, after, 0)
	out := make([]string, 0, len(paths))
	for p := range paths {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// joinPath appends a key segment to a dotted path. Empty prefix
// returns the key unchanged so the topmost level renders as `foo`
// rather than `.foo`.
func joinPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

// bytesEqual returns true iff a and b are byte-identical. Hand-rolled
// instead of bytes.Equal so the apply package doesn't pull in `bytes`.
func bytesEqual(a, b []byte) bool {
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

// BatchMutatorArgs groups a verb + its MutatorArgs for use in ApplyBatchAndPersist.
type BatchMutatorArgs struct {
	Verb string
	Args MutatorArgs
}

// ApplyBatchAndPersist applies a sequence of mutators to the workflow at path
// in a single atomic operation: one lock acquisition, all mutators run
// sequentially against the SAME in-memory document, ONE post-batch CUE
// validation pass, and ONE atomic write.
//
// Contrast with the old save-step-batch loop which acquired/released the lock
// once per phase (F-1) and validated each phase against the on-disk file
// independently before writing (F-2). Both problems are fixed here:
//   - The caller acquires the lock ONCE before calling this function and holds
//     it until it returns (or the caller owns the lock lifecycle — see note).
//   - All mutators run against the same raw map so earlier phases' changes are
//     visible to later phases' validation.
//   - A single post-batch CUE pass replaces N independent pre-flight dry-runs.
//   - If CUE validation fails, NO write has occurred — workflow.json mtime is
//     guaranteed unchanged (F-4).
//
// Lock note: this function does NOT acquire the advisory lock. The caller MUST
// hold it for the duration of this call. This matches ApplyAndPersist's
// contract and keeps the lock lifecycle in the cobra layer where lock timeout,
// --no-lock, and audit lines all live.
//
// On any error the function returns before AtomicWrite so the file is never
// partially mutated.
func ApplyBatchAndPersist(path string, batch []BatchMutatorArgs) ([]ApplyResult, error) {
	if len(batch) == 0 {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read workflow: %w", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse workflow map: %w", err)
	}
	if raw == nil {
		raw = map[string]any{}
	}

	var beforeViolations []schema.Violation
	if len(data) > 0 {
		beforeViolations = schema.ValidateWorkflow(data).Violations
	}

	results := make([]ApplyResult, 0, len(batch))
	for _, b := range batch {
		mut, ok := Mutators[b.Verb]
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrUnknownVerb, b.Verb)
		}
		var res ApplyResult
		if mErr := mut(raw, b.Args, &res); mErr != nil {
			return nil, fmt.Errorf("batch mutator %q: %w", b.Verb, mErr)
		}
		results = append(results, res)
	}

	// Single post-batch CUE validation pass.
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal workflow for validation: %w", err)
	}

	envBypass := os.Getenv("BROWZER_NO_SCHEMA_CHECK") == "1"
	if !envBypass {
		validation := schema.ValidateWorkflow(encoded)
		if !validation.Valid {
			tagged := schema.TagViolationScopes(beforeViolations, validation.Violations)
			return nil, fmt.Errorf("batch schema validation failed:\n%s",
				schema.FormatViolations(tagged))
		}
	}

	var typed Workflow
	if err := json.Unmarshal(encoded, &typed); err != nil {
		return nil, fmt.Errorf("re-parse workflow for validation: %w", err)
	}
	if errs := Validate(typed); len(errs) > 0 {
		return nil, fmt.Errorf("batch validation error: %s: %s", errs[0].Path, errs[0].Message)
	}

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal workflow: %w", err)
	}
	out = append(out, '\n')

	// FR-3: skip rotation when any arg in the batch has NoBackup set.
	// (In practice the batch is from a single caller context, so if
	// one entry opts out, the whole batch opts out.)
	noBackup := false
	for _, b := range batch {
		if b.Args.NoBackup {
			noBackup = true
			break
		}
	}
	if !noBackup {
		rotateWorkflowBackups(path)
	}

	if err := AtomicWrite(path, out); err != nil {
		return nil, err
	}

	for i := range results {
		results[i].ValidatedOk = true
	}
	return results, nil
}

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
// backfill-elapsed on a workflow with all elapsed values already set is a no-op).
// When a Mutator decides "nothing to do", it returns nil with
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

	// Pre-mutation validation snapshot (best-effort). Used downstream
	// by schema.TagViolationScopes to mark each post-mutation
	// violation as `[scope=patch]` (introduced by THIS write) or
	// `[scope=existing]` (already present in the on-disk document).
	// Failures here are silently ignored — a malformed pre-state
	// degrades to "tag everything as patch", matching the conservative
	// default and preserving the historic non-tagged output.
	var beforeViolations []schema.Violation
	if len(data) > 0 {
		beforeViolations = schema.ValidateWorkflow(data).Violations
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
			tagged := schema.TagViolationScopes(beforeViolations, validation.Violations)
			return ApplyResult{}, fmt.Errorf("schema validation failed:\n%s",
				schema.FormatViolations(tagged))
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

	// B6 (2026-05-05, retention reduced 5→2 in 2026-05-07): rotate
	// up to `workflowBackupCount` numeric backups before the rename
	// so a wedged operator can recover the previous workflow state
	// from `.bak.1` … `.bak.<workflowBackupCount>`. Best-effort: rotation failures
	// log to stderr but never abort the mutation. Skipped on no-op
	// mutators (already short-circuited above) and for the daemon-
	// goroutine path (which calls ApplyAndPersist with awaitDurability=
	// true; we still want backups there). Dry-run callers route
	// through ApplyDryRun and never touch disk.
	// FR-3: --no-backup skips rotation (opt-in for autosave hook path).
	if !args.NoBackup {
		rotateWorkflowBackups(path)
	}

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

// workflowBackupCount caps the rotated copies. 2 keeps disk overhead
// minimal while still offering a one-step-back safety net for the
// "Ctrl+Z one workflow ago" use case (each copy ≤200 KB in practice).
// Mirrored as a constant so tests can introspect.
const workflowBackupCount = 2

// rotateWorkflowBackups maintains a rolling backup of `path`:
//
//	<path>.bak.2  ← removed
//	<path>.bak.1  → <path>.bak.2
//	<path>        → <path>.bak.1   (copy, not move — leaves original
//	                                in place so the rename below
//	                                replaces a real file rather than a
//	                                missing one)
//
// Rotations are best-effort. Each failed rename is logged to stderr
// once (not warn-once: the next mutation may have a different
// failure pattern that's worth surfacing). When `path` doesn't
// exist (first-write scenario), the function exits silently.
func rotateWorkflowBackups(path string) {
	if _, err := os.Stat(path); err != nil {
		// No source file → no backup. First-write or daemon-goroutine
		// running against a non-existent path; either way nothing to do.
		return
	}
	// Rotate from oldest to newest so we never overwrite a slot that
	// hasn't been moved yet.
	for i := workflowBackupCount; i >= 2; i-- {
		from := fmt.Sprintf("%s.bak.%d", path, i-1)
		to := fmt.Sprintf("%s.bak.%d", path, i)
		if _, err := os.Stat(from); err != nil {
			continue
		}
		if rmErr := os.Remove(to); rmErr != nil && !os.IsNotExist(rmErr) {
			_, _ = fmt.Fprintf(os.Stderr,
				"warn: backup rotate: remove %s: %v\n", to, rmErr)
			continue
		}
		if rnErr := os.Rename(from, to); rnErr != nil {
			_, _ = fmt.Fprintf(os.Stderr,
				"warn: backup rotate: rename %s → %s: %v\n", from, to, rnErr)
		}
	}
	// Copy current → .bak.1. Use copy (not rename) so the existing
	// AtomicWrite/atomicWriteFsync path replaces a real file at
	// `path` rather than re-creating one — keeps inode metadata
	// stable across the rotation window.
	bak1 := path + ".bak.1"
	if rmErr := os.Remove(bak1); rmErr != nil && !os.IsNotExist(rmErr) {
		_, _ = fmt.Fprintf(os.Stderr,
			"warn: backup rotate: remove %s: %v\n", bak1, rmErr)
		return
	}
	if data, err := os.ReadFile(path); err == nil {
		if writeErr := os.WriteFile(bak1, data, 0o644); writeErr != nil {
			_, _ = fmt.Fprintf(os.Stderr,
				"warn: backup rotate: write %s: %v\n", bak1, writeErr)
		}
	}
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

	// RETRO PR 6 §2.2 — auto-project Explorer rich shape to lean before CUE.
	// Only TASK steps carry an explorer payload; the helper is a no-op for
	// any other step.name. Skipped under BROWZER_EXPLORER_LEGACY_REJECT=1.
	if name, _ := stepMap["name"].(string); name == "TASK" {
		if taskMap, ok := stepMap["task"].(map[string]any); ok {
			if projectExplorerRichToLean(taskMap) {
				out.ExplorerProjected = true
			}
		}
	}

	stepsRaw := raw["steps"]
	stepsSlice, _ := stepsRaw.([]any)
	stepsSlice = append(stepsSlice, stepMap)
	raw["steps"] = stepsSlice

	RecomputeCountersRaw(raw)

	if id, _ := stepMap["stepId"].(string); id != "" {
		out.StepID = id
	}
	return nil
}

// mutatorAppendSteps is the plural sibling of mutatorAppendStep. The
// payload is a JSON array of step objects; each is appended in order
// against the same in-memory map, the counters are recomputed once at
// the end, and ApplyAndPersist runs ONE CUE validation against the
// final document. The advisory lock + tmp-rename + parent-dir-fsync
// pipeline is shared with append-step.
//
// out.StepID is set to the LAST step's stepId so the audit line
// reports the trailing edge of the batch.
//
// Errors:
//   - empty payload
//   - payload is not a JSON array
//   - payload is an empty array (callers composing the array from a
//     template should fail loudly when the template expanded to zero
//     entries instead of silently writing a no-op)
//   - any element is not a JSON object
//
// Closes RETRO 9.1.5 from the 2026-05-05 orchestrate-task-delivery
// session: appending N steps required N round-trips through the daemon
// (one advisory-lock acquisition per step, one validation per step).
// For an 11-task TASKS_MANIFEST that is 11× the lock contention. With
// this verb the operator pays one round-trip + one validation.
func mutatorAppendSteps(raw map[string]any, args MutatorArgs, out *ApplyResult) error {
	if len(args.Payload) == 0 {
		return fmt.Errorf("append-steps: payload is required")
	}
	var stepsPayload []any
	if err := json.Unmarshal(args.Payload, &stepsPayload); err != nil {
		return fmt.Errorf("append-steps: payload must be a JSON array of step objects: %w", err)
	}
	if len(stepsPayload) == 0 {
		return fmt.Errorf("append-steps: payload array is empty (would be a no-op)")
	}

	// Pre-mutation validation: every element must be an object AND must
	// carry a non-empty string stepId. Validating UP-FRONT (before
	// touching raw["steps"]) means the audit-line emitted on the
	// caller's failure path correctly reflects the rejection — without
	// this, a missing-stepId entry in the middle of a batch would let
	// the function set `out.StepID` from an earlier element's id,
	// producing a misleading audit line that names a step the operator
	// never intended to be "the last appended".
	for i, entry := range stepsPayload {
		stepMap, ok := entry.(map[string]any)
		if !ok {
			return fmt.Errorf("append-steps: payload[%d] is not a JSON object", i)
		}
		id, _ := stepMap["stepId"].(string)
		if id == "" {
			return fmt.Errorf("append-steps: payload[%d] missing required string stepId", i)
		}
	}

	stepsRaw := raw["steps"]
	stepsSlice, _ := stepsRaw.([]any)
	var lastStepID string
	projectedAny := false
	// CRITICAL: assignment of stepsSlice back into raw["steps"] happens
	// AFTER the loop. Moving it inside the loop breaks the
	// all-or-nothing rollback contract — a mid-batch error would leave
	// raw["steps"] partially mutated and visible to any downstream
	// validation that reads raw before re-marshal.
	for _, entry := range stepsPayload {
		stepMap := entry.(map[string]any) // safe: validated above
		// RETRO PR 6 §2.2 — auto-project Explorer rich shape to lean
		// per TASK step. Same contract as the singular append-step.
		if name, _ := stepMap["name"].(string); name == "TASK" {
			if taskMap, ok := stepMap["task"].(map[string]any); ok {
				if projectExplorerRichToLean(taskMap) {
					projectedAny = true
				}
			}
		}
		stepsSlice = append(stepsSlice, stepMap)
		lastStepID = stepMap["stepId"].(string) // safe: validated above
	}
	raw["steps"] = stepsSlice

	RecomputeCountersRaw(raw)

	out.StepID = lastStepID
	out.ExplorerProjected = projectedAny
	return nil
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
// has a status in {COMPLETED, SKIPPED, STOPPED}. Used by
// stampWorkflowTotalElapsedIfFinal.
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
// stamping actually occurred — idempotent re-entries return true).
func stampWorkflowTotalElapsedIfFinal(raw map[string]any, stepMap map[string]any, now string) bool {
	// Idempotency guard for the stamp itself.
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

// renameTaskExplorerDomain rewrites every occurrence of `from` → `to` inside
// task.explorer.domains[] AND task.explorer.skillsFound[].domain. Returns true
// when any field actually changed (so the caller can record idempotent NoOps
// distinct from real edits). Used by the rename-domain branch of
// reapply-additional-context (RETRO PR 6 §5.6).
//
// Closes the gap where the existing kind="corrected" mutator only operated on
// task.scope[] — leaving Reviewer-emitted domain typos like "fastapi-backend"
// unrewritten in task.explorer.domains and task.explorer.skillsFound[].domain.
func renameTaskExplorerDomain(taskMap map[string]any, from, to string) bool {
	if from == to || from == "" || to == "" {
		return false
	}
	explorerRaw, ok := taskMap["explorer"]
	if !ok {
		return false
	}
	explorerMap, ok := explorerRaw.(map[string]any)
	if !ok {
		return false
	}

	changed := false

	// task.explorer.domains[] — rewrite each matching entry. Idempotent: if
	// `to` is already present and `from` is also present, the slice keeps a
	// single entry (deduplicate-on-rewrite).
	if domainsRaw, ok := explorerMap["domains"]; ok {
		if domains, ok := domainsRaw.([]any); ok {
			seen := make(map[string]struct{}, len(domains))
			renamed := make([]any, 0, len(domains))
			for _, d := range domains {
				ds, isStr := d.(string)
				if !isStr {
					renamed = append(renamed, d)
					continue
				}
				if ds == from {
					ds = to
					changed = true
				}
				if _, dup := seen[ds]; dup {
					continue
				}
				seen[ds] = struct{}{}
				renamed = append(renamed, ds)
			}
			explorerMap["domains"] = renamed
		}
	}

	// task.explorer.skillsFound[].domain — rewrite the field on each matching
	// SkillFound entry. Touching .domain in-place keeps the rest of the entry
	// (skill, relevance) intact.
	if skillsRaw, ok := explorerMap["skillsFound"]; ok {
		if skills, ok := skillsRaw.([]any); ok {
			for _, sf := range skills {
				sfMap, ok := sf.(map[string]any)
				if !ok {
					continue
				}
				if dom, _ := sfMap["domain"].(string); dom == from {
					sfMap["domain"] = to
					changed = true
				}
			}
		}
	}

	return changed
}

// projectExplorerRichToLean is the §2.2 (RETRO PR 6) auto-projection that
// translates the rich shape Explorer subagents are documented to emit into
// the lean shape #TaskExplorer persists. The translation runs once per
// payload, immediately before the CUE pass — the rich shape is for agent
// reasoning (anchors + reasons keep Reviewer's signal high), the lean shape
// is for persistence (small file, stable schema).
//
// Translations applied to `task.explorer`:
//
//	filesModified: [{path, anchor?, imports?, importedBy?, new?}, ...]
//	  → ["pkg/foo.go", ...] (extract `.path` from each object; preserve
//	    bare strings unchanged)
//	filesToRead:   [{path, reason?}, ...]
//	  → ["pkg/foo.go", ...]
//	depsGraph:     {"pkg/foo.go": {forward:[...], reverse:[...]}}
//	  → {"pkg/foo.go": {imports:[...], importedBy:[...]}} (rename keys; keep
//	    bare imports/importedBy unchanged when already present)
//
// Returns true if ANY field was actually projected (so callers can decide
// whether to set explorerProjected=true on the audit line).
//
// Disable via env BROWZER_EXPLORER_LEGACY_REJECT=1 — when set, the projection
// is skipped and the original payload reaches the CUE validator unchanged
// (which will reject it). Operators / CI can use the flag to detect drift
// in subagent prompts that emit the rich shape (the §2.4 audit relies on
// the lean shape passing without projection).
func projectExplorerRichToLean(taskMap map[string]any) bool {
	if os.Getenv("BROWZER_EXPLORER_LEGACY_REJECT") == "1" {
		return false
	}
	explorerRaw, ok := taskMap["explorer"]
	if !ok {
		return false
	}
	explorerMap, ok := explorerRaw.(map[string]any)
	if !ok {
		return false
	}
	changed := false

	flatten := func(field string) {
		raw, ok := explorerMap[field]
		if !ok {
			return
		}
		entries, ok := raw.([]any)
		if !ok {
			return
		}
		out := make([]any, 0, len(entries))
		fieldChanged := false
		for _, entry := range entries {
			switch v := entry.(type) {
			case string:
				out = append(out, v)
			case map[string]any:
				if path, _ := v["path"].(string); path != "" {
					out = append(out, path)
					fieldChanged = true
				}
				// objects without a path are dropped silently — no
				// salvageable lean form.
			default:
				// unknown type — preserve so the CUE validator
				// produces a precise error instead of us swallowing it.
				out = append(out, entry)
			}
		}
		if fieldChanged {
			explorerMap[field] = out
			changed = true
		}
	}
	flatten("filesModified")
	flatten("filesToRead")

	// depsGraph[path].forward / .reverse → .imports / .importedBy. The
	// CUE schema (#TaskExplorer.depsGraph) keys on imports/importedBy;
	// subagent preambles document the natural-language pair forward/reverse
	// (because they read better as "blast radius forward/reverse" in the
	// agent's head). Rename in-place so the agent docs and the CUE schema
	// can both stay correct without forcing the operator to translate.
	if depsRaw, ok := explorerMap["depsGraph"]; ok {
		if depsMap, ok := depsRaw.(map[string]any); ok {
			for k, v := range depsMap {
				node, ok := v.(map[string]any)
				if !ok {
					continue
				}
				if fwd, ok := node["forward"]; ok {
					if _, alreadyHas := node["imports"]; !alreadyHas {
						node["imports"] = fwd
					}
					delete(node, "forward")
					changed = true
				}
				if rev, ok := node["reverse"]; ok {
					if _, alreadyHas := node["importedBy"]; !alreadyHas {
						node["importedBy"] = rev
					}
					delete(node, "reverse")
					changed = true
				}
				depsMap[k] = node
			}
		}
	}

	return changed
}

// mutatorBackfillElapsed walks every step in the workflow and stamps
// elapsedMin = (completedAt - startedAt) / 60 (minutes) when both timestamps
// are set AND the current elapsedMin is null/missing/zero. Idempotent: running
// twice on the same workflow is a no-op the second time (because the first
// run already populated every fillable cell, so the guard short-circuits).
//
// After per-step backfill, the workflow-level totalElapsedMin is recomputed by
// summing per-step elapsedMin (preferred — matches the per-step values the
// operator just saw). If no steps have an elapsedMin we fall back to
// (updatedAt - startedAt) when both root timestamps are present.
//
// The motivation (RETRO §2): the orchestrator's Step 7 closure was the only
// place that stamped elapsedMin; if the orchestrator was interrupted before
// closure ran, completed steps ended up with elapsedMin: null. This verb is
// the recovery path — runnable any time, including mid-flow.
//
// F-15 cost note: this verb iterates every step (O(steps)) holding the
// workflow advisory flock. Today's workflows are O(20) steps so the lock-
// hold window is sub-millisecond, but invoking it on a very large
// workflow.json (hundreds of steps) over the daemon's --await path will
// block sibling mutators queued behind it for the duration of the walk.
// The verb's `Long` cobra string surfaces this cost to operators.
func mutatorBackfillElapsed(raw map[string]any, _ MutatorArgs, out *ApplyResult) error {
	stepsRaw, _ := raw["steps"].([]any)
	changed := 0
	for _, s := range stepsRaw {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		// Only backfill when the current value is null / missing / zero.
		if !elapsedMinIsUnfilled(sm["elapsedMin"]) {
			continue
		}
		completedRaw, ok := sm["completedAt"].(string)
		if !ok || completedRaw == "" {
			continue
		}
		// stampElapsedMin is no-op when startedAt is missing/malformed —
		// snapshot the cell before the call so we only count real updates.
		before := sm["elapsedMin"]
		stampElapsedMin(sm, completedRaw)
		if !sameElapsedCell(before, sm["elapsedMin"]) {
			changed++
		}
	}

	// Recompute workflow-level totalElapsedMin from per-step values when we
	// have any signal; otherwise fall back to (updatedAt - startedAt).
	totalChanged := recomputeTotalElapsedFromSteps(raw)

	if changed == 0 && !totalChanged {
		out.NoOp = true
		out.NoOpReason = "no_unfilled_elapsed"
		return nil
	}
	raw["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
	return nil
}

// elapsedMinIsUnfilled returns true when the given JSON-decoded value should
// be treated as "missing" by the backfill — null, absent, or numeric zero.
// Any positive value is left alone (a previous backfill already stamped a
// real elapsed value).
func elapsedMinIsUnfilled(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case float64:
		return x == 0
	case int:
		return x == 0
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return false
		}
		return f == 0
	}
	return false
}

// sameElapsedCell compares two elapsedMin cell values structurally. Used to
// detect whether stampElapsedMin actually wrote a new value.
func sameElapsedCell(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	af, aok := toFloat(a)
	bf, bok := toFloat(b)
	if aok && bok {
		return af == bf
	}
	return false
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// recomputeTotalElapsedFromSteps refreshes raw["totalElapsedMin"] from the
// per-step elapsedMin values. Returns true when the value changed.
//
// Order of preference:
//  1. Sum of all positive per-step elapsedMin values (matches what the
//     operator just saw on each step).
//  2. MAX(per-step completedAt) - startedAt at the workflow root, when (1)
//     yielded zero. We deliberately avoid raw["updatedAt"] as the upper
//     bound because backfill itself stamps updatedAt; using it conflates
//     "time backfill ran" with "wall-clock spent on the feature" and
//     overstates totalElapsedMin dramatically on workflows that have been
//     idle for weeks before recovery.
func recomputeTotalElapsedFromSteps(raw map[string]any) bool {
	stepsRaw, _ := raw["steps"].([]any)
	sum := 0.0
	for _, s := range stepsRaw {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if f, ok := toFloat(sm["elapsedMin"]); ok && f > 0 {
			sum += f
		}
	}
	if sum == 0 {
		// F-5 fallback: derive from workflow startedAt → MAX(per-step
		// completedAt). Survives idle gaps because completedAt is only
		// stamped when the orchestrator closed the step, not by backfill.
		started, _ := raw["startedAt"].(string)
		var maxCompleted string
		for _, s := range stepsRaw {
			sm, ok := s.(map[string]any)
			if !ok {
				continue
			}
			c, _ := sm["completedAt"].(string)
			if c == "" {
				continue
			}
			if maxCompleted == "" || c > maxCompleted { // RFC3339 strings sort lexicographically
				maxCompleted = c
			}
		}
		if started != "" && maxCompleted != "" {
			startedT, err1 := time.Parse(time.RFC3339, started)
			if err1 != nil {
				startedT, err1 = time.Parse(time.RFC3339Nano, started)
			}
			completedT, err2 := time.Parse(time.RFC3339, maxCompleted)
			if err2 != nil {
				completedT, err2 = time.Parse(time.RFC3339Nano, maxCompleted)
			}
			if err1 == nil && err2 == nil {
				delta := completedT.Sub(startedT).Minutes()
				if delta > 0 {
					sum = delta
				}
			}
		}
	}
	current, _ := toFloat(raw["totalElapsedMin"])
	if current == sum {
		return false
	}
	raw["totalElapsedMin"] = sum
	return true
}

// RecomputeCountersRaw recomputes totalSteps, completedSteps, updatedAt, and
// currentStepId from the in-memory raw document. Called after every mutation
// that alters the steps slice or step statuses (append-step, append-steps,
// save-step). currentStepId is set to the stepId
// of the first RUNNING or PENDING step after the last COMPLETED one, or empty
// when all steps are in a terminal status.
func RecomputeCountersRaw(raw map[string]any) {
	stepsRaw := raw["steps"]
	stepsSlice, _ := stepsRaw.([]any)
	total := len(stepsSlice)
	completed := 0
	var currentStepID string
	for _, s := range stepsSlice {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		status, _ := sm["status"].(string)
		if status == StatusCompleted {
			completed++
		} else if currentStepID == "" {
			// First non-completed (non-terminal) step is the "current" one.
			sid, _ := sm["stepId"].(string)
			currentStepID = sid
		}
	}
	raw["totalSteps"] = total
	raw["completedSteps"] = completed
	raw["currentStepId"] = currentStepID
	raw["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
}
