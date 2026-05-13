# Changelog

All notable user-facing changes to the `browzer` Go CLI and the workflow-v1
CUE schema are documented in this file. The CLI follows semantic versioning
(`cli-vX.Y.Z` tags); individual line items are grouped by release window.

## Unreleased

### Added

- **`browzer plugin doctor`**: new Cobra subcommand. Reads `~/.claude/plugins/installed_plugins.json` and `~/.claude.json` to validate plugin enablement. Exit 0 = PASS, exit 1 = FAIL; warnings are soft (non-fatal). Useful as a preflight before any skill-driven session.
- **`browzer gain --include-internal`**: `browzer gain --by method` now excludes `workflow-audit:*` sources by default (these are internal bookkeeping events, not operator-visible savings). Pass `--include-internal` to restore the previous aggregate-all behaviour. `QueryAggregated(start, end, includeInternal)` gained a third boolean parameter accordingly.
- **New runfilters**: `pnpm install`, `terraform plan`, `kubectl get pods` are now classified and compressed by the filter pipeline.
- **Stop hook `browzer-session-summary.mjs`**: injects a per-session summary line `[browzer] Session saved Nk tokens (adoption Mx). Top wasted: <source> (<Yk>).` as `additionalContext` on every `Stop` event.

### Changed

- **`TrackRun` is now a 5-arg function** (`source, raw, compressed, filterLevel, execMs`): previously a 3-arg stub, it now wires directly to `daemon.Client.Track` with `EstimationMethod="measured"` and a 50 ms context deadline. Callers that passed only 3 arguments must add `filterLevel` and `execMs`.
- **`git diff` uses hunk-aware semantic compression**: the previous hard truncation at 150 lines is replaced by per-hunk semantic trimming; long diffs are reduced without cutting inside a hunk boundary.
- **Source name fix — `wasted-rg`**: plugin hook events emitted for `rg` (ripgrep) invocations were incorrectly tagged `wasted-grep`. The correct source is now `wasted-rg`. Existing `browzer gain` aggregates from before this fix will retain the old label in the SQLite store.

### Added

- **`browzer save-step --hint-fixes`** (R-5): on validation failure, emits a worked example for each enum / unknown-field violation in the form `expected one of [a, b, c]; example: "<field>": "<a>"`. Default behaviour (no flag) is unchanged. Driven by the new `FormatViolationsWithHints` helper in `internal/schema/validator.go` and a shared `formatViolationsCore(emitHints bool)` private renderer.
- **`browzer save-step-batch --from PHASE=PATH ...`** (R-6): persists multiple phase payloads atomically. All `--from` entries are CUE-validated against the in-memory mutated document under a single advisory flock; on any individual failure the batch rolls back without writing. Duplicate phase names emit `cliExitErr(2)`. Backed by a new `wf.ApplyBatchAndPersist` helper in `internal/workflow/apply.go`.
- **`browzer workflow init --mode <autonomous|review>`** (R-1 / R-8): seeds `config.mode` directly at workflow init time. Default `autonomous`. The `BootstrapOptions.Mode` field is enum-validated against `{autonomous, review}`; bogus values exit non-zero.
- **CUE schema — `granularityWarnings[]` on `#TasksManifest`** (R-15): optional array of `{taskId, verdict (collapse|split), reason}` entries emitted by `generate-task` when a task carries `<2 files` (collapse) or `>10 files` (split). Backward-compatible — existing fixtures continue to validate.
- **COMMIT regression fixtures** (R-14): `schemas/fixtures/valid/commit-min.json` and `schemas/fixtures/invalid/commit-bad-name.json`. Exercised by `internal/schema/commit_roundtrip_test.go` (CUE round-trip) and `internal/commands/workflow_save_step_r5_r6_test.go::TestSaveStep_CommitRoundtrip_PersistsAndReads` (command-level round-trip via `save-step` + `get-step`).
- **Granularity negative fixture** (`schemas/fixtures/invalid/granularity-bad-verdict.json`): exercises `verdict = "merge"` (rejected by the `collapse|split` enum). Schema vet now reports 13 valid + 19 invalid fixtures.

### Changed

- **`save-step` validator**: `DryRunViolations` and `ApplyDryRun` now share a `runMutatorInMemory` helper (no more 30-line preamble duplication). `DryRunViolations` returns `(nil, mErr)` on mutator error instead of silently swallowing it as `(nil, nil)`.
- **`save-step` cobra surface**: removed dead `fromStdin bool` parameter from `readSaveInput`; the function infers stdin mode from `fromFile == ""`. Resolves `unusedparams` lint.

## cli-v3.0.0 — 2026-05-07 — Collapse workflow surface to `get-step` + `save-step` (BREAKING)

### BREAKING

- **Deleted workflow mutator verbs (16)**: `workflow {patch, query (legacy 10-name catalogue), get-config, set-config, set-status, set-current-step, complete-step, update-step, audit-model-override, reapply-additional-context, truncation-audit, append-dispatch, append-dispatches, append-agent, append-review-history, save}`. Migration: skill bodies stage their phase output to `docs/browzer/<feat>/staging/<PHASE>.{md,json}` via `Write`; the Browzer plugin's `PostToolUse(Write)` autosave hook calls `browzer save-step <PHASE> --id <feat> --from <file>`, which CUE-validates and persists atomically.
- **Deleted named queries (10)**: `reused-gates`, `failed-findings`, `open-deferred-actions`, `task-gates-baseline`, `changed-files`, `deferred-scope-adjustments`, `open-findings`, `next-step-id`, `cache-warm-deps`, `cache-warm-mentions`. Survivors (3): `tasks-manifest`, `steps-by-name`, `steps-by-owner`.
- **`browzer get-step` flag surface change**: removed `--field`, `--render`, `--bash-vars`, `--finding`. Default output is now rich markdown rendered from one of 11 embedded `view/templates/*.md.tmpl` files (one per phase + a `generic` fallback). `--json` returns the new `#StepView` payload defined in `packages/cli/schemas/workflow-v1.cue`.
- **Schema codegen**: `make -C packages/cli/schemas all` no longer generates `packages/skills/references/workflow-schema.md` — that target and the `MD_OUT` / `MD_RENDER` Make variables are gone. The skills-side mirror file and the `sync-shared-refs.mjs` script were deleted; skills now query the schema at runtime via `browzer workflow describe-step-type <NAME> --json`.

### Added

- **`browzer get-step <ID> --id <feat>`** (top-level + `browzer workflow get-step`): rich markdown view formatted for LLM context (role + scope + skills + PRD slice + deps + invariants + done-when), `--json` for the `#StepView` payload. Accepted IDs: `PRD`, `TASKS`, `TASK_NN`, `CODE_REVIEW`, `RECEIVING_CODE_REVIEW`, `WRITE_TESTS`, `UPDATE_DOCS`, `FEATURE_ACCEPTANCE`, `COMMIT`, `BRAINSTORM`.
- **`browzer save-step <PHASE> --id <feat> [--from <file>] [--stdin]`** (NEW top-level): CUE-validates and persists a phase payload into `workflow.json`. Accepts a markdown PRD via the markdown parser or a JSON payload. Flags: `--validate-only`, `--quiet`. Exits silent on success.
- **CUE additions**: `#StepView` definition in `packages/cli/schemas/workflow-v1.cue`.
- **Embedded view templates**: 11 new `internal/workflow/view/templates/*.md.tmpl` files (one per phase plus `generic`), embedded into the Go binary and rendered by `get-step`.

## Unreleased — pre-3.0.0 entries

### BREAKING

- **workflow-v1 schema (B8)**: removed `twoPassRun.mentionsFallback` legacy
  field (`#TwoPassRun.mentionsFallback`). The field carried no active consumer
  after `mentionsFallbackUsed: bool` shipped 2026-04-30; keeping it around
  forced every UPDATE_DOCS step payload to thread a `null` value just to pass
  CUE validation.
  - Migration for any open workflow.json that still carries the field:
    ```bash
    jq 'del(.steps[].updateDocs.twoPassRun.mentionsFallback)' workflow.json \
      > workflow.json.tmp && mv workflow.json.tmp workflow.json
    ```
  - The `mentionsFallbackUsed: bool` flag is unaffected and remains required.

### Added

- **`browzer workflow append-agent <stepId>`**: appends a TaskAgent record
  to a TASK step's `task.execution.agents[]` inside the standard advisory-lock
  + post-mutation CUE-validate window. Replaces ad-hoc `workflow patch --jq`
  splicing in the `execute-task` skill so each specialist result lands as a
  single atomic write. Accepts the record on stdin or via `--payload <file>`.
- **`browzer workflow backfill-elapsed`**: walks every step and computes
  `elapsedMin = (completedAt - startedAt) / 60` when both timestamps are set
  and `elapsedMin` is missing or zero. One-shot recovery for workflows that
  predate `complete-step`'s auto-stamp; safe to re-run.
- **`--quiet` parity on read verbs**: cross-cutting `--quiet` is now wired on
  `status`, `explore`, `search`, `deps`, `mentions`, and `workspace sync` via
  the shared `output.RegisterQuietFlag()` helper (`internal/output/verbosity.go`).
  Default-on under `BROWZER_LLM=1`; previously only the workflow mutators
  honoured the flag.
- **`browzer workflow describe-step-type --inline-enums` (A1.3)**: emits one
  JSON record per enumerable field with `{path, type, values}` (string
  disjunction) or `{path, type, regex}` (regex constraint) so an LLM-driven
  caller can build a payload that passes CUE validation on first try without
  reading the SSOT.
- **`browzer workflow patch --dry-run` (A1.4)**: applies the jq mutation,
  validates against CUE, and prints `{ok, errors, beforeHash, afterHash,
  diffPaths}` to stdout — without writing workflow.json. Combinable with
  `--bulk`.
- **`browzer workflow patch --bulk '<json-array>'` (A3.3)**: applies a
  sequence of jq expressions inside a single advisory-lock window with one
  CUE validation at the end. Combinable with `--dry-run`.
- **`browzer workflow set-finding-status` / `--batch` (A3.1, B5)**: singular
  form `set-finding-status <stepId> <findingId> <status> [--note <text>]`
  and bulk form `set-finding-statuses --batch '[{stepId, findingId, status,
  note?}, ...]'` apply 1+N updates inside a single lock + write.
- **`browzer workflow append-dispatches --batch` (A3.2)**: append N
  dispatch records inside a single lock + write.
- **CUE error-reporter enrichment (A1.5)**: `Violation` now carries
  `allowedFields[]` (when the engine raises `unknown-field`) and
  `allowedValues[]` (when the engine rejects an invalid enum literal).
  Type-mismatch errors against a string-disjunction field are reclassified
  as `invalid-enum-value` so the operator sees the legal set instead of a
  raw `conflicting values "skip" and "deferred …"` line.

### Changed

- **`BROWZER_LLM=1` audit silence (A2)**: every soft warning emitted from
  the workflow-mutator dispatch path now respects the LLM-quiet gate.
  Previously the `workflow.v1` capability fallback line, the daemon-version
  preflight failure, the daemon-protocol-mismatch line and the
  WorkflowMutate failure line all bypassed the gate via `daemonFallbackWarnOnce`
  and consumed agent-context tokens. Real validation/IO errors are NOT
  silenced.
- **Workflow apply pipeline (B4 + B6)**: the standalone-sync path now
  rotates 5 numeric backups (`workflow.json.bak.1` … `.bak.5`) before each
  durable write, and validates the post-mutation payload BEFORE the
  tmp+rename commit so a rejected payload can never produce a half-written
  file. Backup writes are skipped on `--dry-run` and on no-op mutators.
- **Daemon-down fallback warn (B3)**: the warn-once gate is now persisted
  to `$BROWZER_HOME/.daemon-down-marker` (60s TTL) so each new CLI
  invocation does not re-emit the warning. Marker is removed when the
  daemon answers `Daemon.Version` again.
