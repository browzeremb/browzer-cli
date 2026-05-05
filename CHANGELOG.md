# Changelog

All notable user-facing changes to the `browzer` Go CLI and the workflow-v1
CUE schema are documented in this file. The CLI follows semantic versioning
(`cli-vX.Y.Z` tags); individual line items are grouped by release window.

## Unreleased

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
