# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Authoritative references (read first)

- **`docs/CLAUDE_CODE_PLUGIN.md`** — single source of truth for the broader Browzer plugin/CLI consolidation: layout, invariants, CI gates, version trajectory, session-log fixes. Always consult before any non-trivial change here.
- **`packages/skills/`** — the Claude Code plugin that consumes this CLI. Skills are markdown wrappers around `browzer` subcommands. This CLI MUST stay synchronized with that plugin: any change to a subcommand surface (flags, JSON shape, exit codes, named queries, the `get-step` view templates, the `save-step` payload contract) requires patching every skill body that invokes it in the same change. Skill-side audits (`scripts/audit/skill-cli-references.mjs`, `skill-cli-sync-drift.mjs`, `skill-no-unknown-named-query.mjs`, `skill-cobra-arg-syntax.mjs`) gate that drift in the monorepo `quality` job. See `packages/skills/CLAUDE.md`.

## What this package is

`@browzer/cli` is a single-binary Go CLI (`browzer`, current version `3.0.0` — `VERSION`, requires Go **≥ 1.25**) that talks to the Browzer server over HTTPS and to a local Unix-socket daemon over JSON-RPC. It is the runtime side of the Browzer plugin: skills are thin markdown contracts; this CLI is what they actually invoke.

Two surfaces:

1. **RAG / token-economy** — `login`, `workspace {init,sync,index,docs,list,...}`, `explore`, `search`, `deps`, `read`, `gain`, `daemon`, `ask`, `job`. The `read`/`glob`/`grep` rewrites driven by the plugin's hooks land here.
2. **Workflow read + persist** — the surviving workflow surface is intentionally narrow: **reads** via `browzer get-step <ID> --id <feat>` (top-level, also exposed under `browzer workflow get-step`) which by default emits a rich markdown view assembled from embedded `view/templates/*.md.tmpl` (one per phase) and `--json` for the raw `#StepView`; **persists** via `browzer save-step <PHASE> --id <feat> [--from <file>] [--stdin] [--hint-fixes]` which validates the payload against the CUE schema and writes atomically (tmp + rename + optional fsync). `--hint-fixes` makes enum / unknown-field violations emit a worked example (`expected one of [...]; example: "<key>": "<value>"`) — opt-in, off by default. A sibling **`save-step-batch --from PHASE=PATH ...`** persists multiple phases atomically under a single advisory flock + single CUE pass against the in-memory mutated document; failure rolls back without writing. A small set of structured mutators remains for batch / bookkeeping use cases — `workflow {append-step,append-steps,backfill-elapsed,set-finding-statuses,validate,describe-step-type,schema,init}` — each acquiring an advisory flock and CUE-validating before commit. Every other historical mutator verb (`patch`, `query`, `set-config`, `set-status`, `set-current-step`, `complete-step`, `update-step`, `audit-model-override`, `reapply-additional-context`, `truncation-audit`, `append-dispatch{,es}`, `append-agent`, `append-review-history`, `save`, `get-config`) was deleted in CLI v3.0.0; skill bodies that authored those payloads now stage a markdown / JSON file under `docs/browzer/<feat>/staging/<PHASE>.{md,json}` and let the plugin's autosave Write hook call `save-step`.

## Commands

Run from this package directory (`packages/cli/`).

```bash
# Full CI parity — run before every push touching this package.
# vet + race tests + 5 cross-compiles (darwin/linux/windows × arm64/amd64) + golangci-lint v2.5.0
make ci

# Fast inner loop
make test            # go test -race -count=1 ./...
make vet             # go vet ./...
make lint            # golangci-lint run --timeout=5m  (pin: v2.5.0)
make build           # builds to $HOME/.local/bin/browzer

# Single-package / single-test
go test -race ./internal/schema/...
go test -race -run TestValidator_Mutation ./internal/schema/...
go test -race -run TestWorkflowQuery_StepsByName ./internal/commands/...

# Mutation testing (validator + dispatch scope, 30–60min)
make mutate          # writes packages/cli/mutate-out/report.txt (static scope: ./internal/schema/ + workflow_append_dispatch.go + workflow_describe_step_type.go; auto-discovers changed non-test non-schema .go files via git diff origin/main, excluding _test.go and internal/schema/)
```

### CUE schema codegen (`schemas/`)

`schemas/workflow-v1.cue` is the SSOT for `workflow.json`. Everything downstream is generated. **Never** hand-edit `workflow-v1.schema.json`, `cue_types_workflow_gen.go`, or the embed mirrors under `internal/schema/`. The schema also defines `#StepView`, the projection consumed by `browzer get-step --json` and rendered by the embedded `internal/workflow/view/templates/*.md.tmpl` set (one template per phase).

```bash
make -C schemas all          # regenerate every artifact (after editing the .cue)
make -C schemas ci-check     # assert no drift between checked-in artifacts and fresh codegen
make -C schemas vet          # cue vet schema + valid/invalid fixtures
make -C schemas clean        # remove generated artifacts
```

`make ci` calls `make -C schemas ci-check` as step 1b — codegen drift fails CI before vet/test/cross-compile run. The Markdown reference that used to be generated alongside (`packages/skills/references/workflow-schema.md`) was retired in the v3.0.0 refactor — skills consume the schema at runtime via `browzer workflow describe-step-type <NAME> --json` instead of static prose.

## Architecture — big picture

### Layered composition

```
cmd/browzer/main.go
       │
       ▼
internal/commands/        ← Cobra command tree (≈110 files, ≈51 prefixed `workflow_`)
       │
       ├── internal/api/         HTTPS client to the Browzer server (workspaces, search, ask, jobs, billing, org, github_releases, preflight)
       ├── internal/auth/        Device-flow OAuth + API-key login + revoke; credentials at ~/.browzer/credentials
       ├── internal/daemon/      Unix-socket JSON-RPC daemon: filter pipeline, manifest cache, session cache, workflow_queue
       ├── internal/schema/      CUE validator + scaffold emitter + describe-step-type; embeds workflow-v1.cue + workflow-v1.schema.json
       ├── internal/workflow/    Workflow mutators (append/update/complete/...), audit log, render templates
       ├── internal/tracker/     SQLite tracker (savedTokens, per-language coefficients) for `browzer gain`
       ├── internal/cache/       Manifest + session caches consumed by the daemon
       ├── internal/format/      JSON / table / ultra / llm output rendering
       ├── internal/flags/       Shared cobra flag wiring (--json, --save, --ultra, --llm, -v..-vvv, --schema)
       ├── internal/config/      ~/.browzer/config.json + .browzer/config.json (workspace) + .browzer/skills.config.json
       └── internal/{walker,git,upload,urlvalidate,ui,errors,version,output,telemetry}
```

`cmd/browzer/main.go` is a thin entrypoint; everything testable lives in `internal/commands/` and the supporting packages.

### Workflow schema is the SSOT contract

The single most important invariant: **`packages/cli/schemas/workflow-v1.cue` is the only authoritative description of `workflow.json`.** All consumable artefacts are codegen output. The generated chain:

```
workflow-v1.cue
   ├── (cue def --out openapi + scripts/enrich-openapi-projection.mjs)        →  workflow-v1.schema.json
   │                                                                          →  internal/schema/workflow-v1.schema.json (embed mirror)
   ├── (cue exp gengotypes)                                                   →  cue_types_workflow_gen.go
   └── (cp)                                                                   →  internal/schema/workflow-v1.cue (embed mirror)
```

Why two enrichment passes on the JSON Schema:

1. Carry `time.Format(time.RFC3339)` through as `format: "date-time"` so `scaffold.go` can emit RFC3339 placeholders rather than empty strings (otherwise scaffolds fail CUE validation immediately).
2. Align `*default | T` fields with the WF-OPTIONAL-MARKER contract — fields with explicit defaults are not required from operator POV, even though OpenAPI projection marks them required.

`internal/schema/describe.go` honours `cue.Iterator.IsOptional()` (the `?` marker) — audited from the skills side by `scripts/audit/cli-describe-step-type-respects-optional-marker.mjs`.

Schema v2 is the current contract (since 2026-05-04). Workflows tagged `schemaVersion: 1` are read-only legacy; mutator verbs reject writes to them.

### Daemon vs standalone write modes

Every workflow mutator honours three execution modes:

| Mode               | Path                       | Cost                                          |
| ------------------ | -------------------------- | --------------------------------------------- |
| `--sync`           | in-process standalone      | ~500ms (full read/validate/write)             |
| `--async` (default)| daemon FIFO               | <50ms — daemon completes durably in background |
| `--await`          | daemon + fsync wait        | ~50–120ms — durable when the call returns     |

Override via `BROWZER_WORKFLOW_MODE=sync|async|await`. CI/test paths force standalone (`--sync`) when a long-running daemon binary may be stale.

> **`save-step` / `save-step-batch` exception**: `--async` is **rejected** by both verbs with exit code 2 (`--async is deprecated; remove the flag`). The default for these two verbs is `--await` (daemon + fsync), because the very next agent step typically reads the persisted phase. Callers that previously passed `--async` must remove the flag; `--await` is the correct equivalent. Generic mutators still accept `--async` as the default fire-and-forget mode.
>
> **`--async` deprecation timeline**: `--async` will be removed from all workflow mutators (including generic ones) in the next major release (v4.0.0). Callers should migrate to `--await` (durable, same latency class) or `--sync` (in-process, no daemon dependency). To flag deprecated usage in your own call-sites, pass `cmd.Flags().MarkDeprecated("async", "use --await instead")` when wiring new Cobra commands. The environment variable `BROWZER_WORKFLOW_MODE=async` is also deprecated and will be ignored starting in v4.0.0; set `BROWZER_WORKFLOW_MODE=await` or omit it to accept the per-verb default.

The daemon (`internal/daemon/`) speaks JSON-RPC over a Unix socket, hosts the read-filter pipeline (AST trimming for `browzer read --filter auto`), the manifest + session caches, and the `workflow_queue` for `--async` writes. Lifecycle: `browzer daemon start --background`, `browzer daemon status --json`, `browzer daemon stop`.

### LLM/agent-friendly conventions

Every read command supports:

- `--json` — compact JSON to stdout, no banners.
- `--save <file>` — write JSON to a file (implies `--json`, stdout silent — receipts pattern used heavily by skills).
- `--schema` — print the response JSON Schema without hitting the server (discovery without payload).
- `--ultra` — ultra-compact output (smaller payloads, fewer fields).
- `--llm` — LLM mode: suppresses banners, disables colors + spinners.
- `-v`/`-vv`/`-vvv` — verbosity (decisions / subprocess / raw I/O).

Mutators add `--quiet` (default-on under `BROWZER_LLM=1` for every workflow mutator via `quietByDefaultUnderLLM()` at `internal/commands/workflow_mutator_helpers.go:47`). `--quiet` is also wired cross-cutting on the read verbs `status`, `explore`, `search`, `deps`, `mentions`, and `workspace sync` via the shared `output.RegisterQuietFlag()` helper in `internal/output/verbosity.go` so skill bodies can suppress decorative output uniformly.

`describe-step-type` accepts `--include-base` (merge base step fields), `--field <jq-path>` (project a sub-tree), and `--save <path>` (cache the schema slice; skills use `/tmp/<feat>/.schema-cache/<NAME>.json`).

### Step views (markdown + JSON)

`browzer get-step <ID> --id <feat>` is the canonical read surface. Default output is a rich markdown view formatted for LLM context (role + scope + skills + PRD slice + deps + invariants + done-when), assembled by templates embedded at `internal/workflow/view/templates/*.md.tmpl` (11 templates — one per phase plus a `generic` fallback). `--json` emits the underlying `#StepView` payload defined in `schemas/workflow-v1.cue`. Accepted IDs: `PRD`, `TASKS`, `TASK_NN`, `CODE_REVIEW`, `RECEIVING_CODE_REVIEW`, `WRITE_TESTS`, `UPDATE_DOCS`, `FEATURE_ACCEPTANCE`, `COMMIT`, `BRAINSTORM`, plus two virtual phases: `ORIGINAL_REQUEST` (returns `metadata.originalRequest` — the verbatim operator ask captured at `workflow init` time, used as a fallback when a phase like BRAINSTORM was skipped) and `CONFIG` (carries `executionStrategy`, `mode`, `setAt` from `metadata.config` — seeded by `workflow init --execution-strategy <serial|parallel|parallel-worktrees|agent-teams>` and consumed by `generate-task` / orchestrator skills). Neither virtual phase has an underlying `steps[]` entry. Adding a new phase = add a `.md.tmpl` here AND extend `#StepView` in the CUE.

**`--exit-only` flag (R-4):** `browzer get-step <PHASE> --id <feat> --exit-only` is a lightweight existence probe. Both stdout and stderr are suppressed when the step is absent or the phase is unknown — exit code is the sole signal: 0 = step found, 2 = absent or phase unknown. Use it in scripts and skill bodies that need a conditional branch without consuming the full markdown view:

```bash
if browzer get-step CODE_REVIEW --id "$FEAT_ID" --exit-only; then
  # step exists — read it normally
  browzer get-step CODE_REVIEW --id "$FEAT_ID" --json
else
  echo "CODE_REVIEW not yet persisted"
fi
```

Do not parse stdout when `--exit-only` is set — it is empty on both success and failure paths.

### Cross-step aggregations

The historical `browzer workflow query` verb (and its 13-name catalogue — `tasks-manifest`, `steps-by-name`, `steps-by-owner`, plus the deprecated `reused-gates`, `failed-findings`, `open-deferred-actions`, `task-gates-baseline`, `changed-files`, `deferred-scope-adjustments`, `open-findings`, `next-step-id`, `cache-warm-deps`, `cache-warm-mentions`) was deleted in CLI v3.0.0. The data those queries projected now flows through `get-step` markdown / `#StepView` JSON and the `save-step` write path. Skills no longer call `jq` over `workflow.json` directly.

### Tracker / `gain`

`internal/tracker/` is a SQLite store. `savedTokens` is estimated per-language (`tracker/schema.sql` carries per-language coefficients calibrated against Anthropic `count_tokens`; mean absolute error ~14% vs the previous flat `÷4` heuristic at ~35%). When the calibration corpus changes, regenerate coefficients — Family-4 models share the tokenizer so one model suffices.

### Exit codes

| Code | Meaning                                 |
| ---- | --------------------------------------- |
| 0    | Success                                 |
| 1    | Generic error                           |
| 2    | Not authenticated → `browzer login`     |
| 3    | No Browzer project → `browzer init`     |
| 4    | Not found (workspace / document)        |
| 10   | CLI outdated → `browzer upgrade`        |
| 130  | SIGINT                                  |
| 143  | SIGTERM                                 |

These are part of the contract — skills branch on them. Don't repurpose.

## Conventions when editing this package

- **Go version**: `go.mod` requires **1.25.0+**. `make ci` enforces this in step 0.
- **Lint**: `golangci-lint v2.5.0` exactly. `make ci` auto-installs the pinned version into `$(go env GOPATH)/bin` if missing — do not float to a newer version locally.
- **CUE edits → run `make -C schemas all`** before committing. CI's `make ci-check` will fail otherwise. No skill-side mirror sync is needed — schema-describing prose was removed from skill bodies in v3.0.0.
- **Adding a new step phase**: extend `#StepView` in `schemas/workflow-v1.cue`, add a sibling `.md.tmpl` under `internal/workflow/view/templates/` (consumed by the embedded templates in `get-step`), add the parser branch in `workflow_save_step.go`, and document the ID in the `get-step` table. Skills cannot consume it until skill bodies + `skill-cli-references.mjs` audit allowlists are updated in the same change.
- **Adding a new mutator verb** (rare — prefer extending `save-step` / `get-step`): wire it under `internal/commands/workflow_*.go`, register validation against the post-mutation CUE shape, default `--quiet` on under `BROWZER_LLM=1`, and document it in the README's "Workflow state" table.
- **Adding a CLI flag visible to skills**: update the README, audit `skill-cobra-arg-syntax.mjs` examples, and rerun `skill-cli-sync-drift.mjs`.
- **Plugin host-agnosticism**: a pre-push audit `audit-skill-host-agnosticism` (`scripts/packages/skills/audit/skill-host-agnosticism.mjs`) ensures `packages/skills/` contains no monorepo-specific paths (e.g. `packages/cli/internal/`, `apps/api/`) in SKILL.md or hook prose. The plugin is mirrored to `browzeremb/skills`; this audit is mandatory before any PR that touches skill bodies or hooks.
- **JSON output stability**: every read command's `--json` payload is consumed by skills as a contract. Field renames are breaking changes; gate behind a major bump (cli-v2.0.0 was the last BREAKING — `#TestSpec.type → intent`, Explorer rich→lean projection, `--quiet` parity on `explore`/`search`).
- **Banners go to stderr** (regression-pin test exists). Stdout is reserved for command output.
- **JSON encoding**: read paths that re-encode workflow JSON use `marshalNoHTMLEscape` (`json.Encoder` with `SetEscapeHTML(false)`) — needed because earlier versions hit a parse race on PRDs ~35 KB. There's a 50-iter regression test (`TestWorkflowQuery_StepsByName_AfterRecentAppendStep_ProducesValidJSON`); keep it green.
- **Daemon detach**: platform-split between `daemon_detach_unix.go` and `daemon_detach_windows.go`. Don't merge.
