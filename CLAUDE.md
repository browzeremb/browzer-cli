# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Authoritative references (read first)

- **`docs/CLAUDE_CODE_PLUGIN.md`** — single source of truth for the broader Browzer plugin/CLI consolidation: layout, invariants, CI gates, version trajectory, session-log fixes. Always consult before any non-trivial change here.
- **`packages/skills/`** — the Claude Code plugin that consumes this CLI. Skills are markdown wrappers around `browzer` subcommands. This CLI MUST stay synchronized with that plugin: any change to a subcommand surface (flags, JSON shape, exit codes, named queries, mutator verbs, `--render` templates) requires patching every skill body that invokes it in the same change. Skill-side audits (`scripts/audit/skill-cli-references.mjs`, `skill-cli-sync-drift.mjs`, `skill-no-unknown-named-query.mjs`, `skill-cobra-arg-syntax.mjs`) gate that drift in the monorepo `quality` job. See `packages/skills/CLAUDE.md`.

## What this package is

`@browzer/cli` is a single-binary Go CLI (`browzer`, current version `2.2.0` — `VERSION`, requires Go **≥ 1.25**) that talks to the Browzer server over HTTPS and to a local Unix-socket daemon over JSON-RPC. It is the runtime side of the Browzer plugin: skills are thin markdown contracts; this CLI is what they actually invoke.

Two surfaces:

1. **RAG / token-economy** — `login`, `workspace {init,sync,index,docs,list,...}`, `explore`, `search`, `deps`, `read`, `gain`, `daemon`, `ask`, `job`. The `read`/`glob`/`grep` rewrites driven by the plugin's hooks land here.
2. **Workflow state mutator** — the `workflow {append-step,append-steps,update-step,complete-step,set-status,set-config,set-current-step,patch,query,get-step,get-config,append-review-history,reapply-additional-context,audit-model-override,truncation-audit,validate,describe-step-type,scaffold,...}` verbs are the **only** sanctioned writers of `docs/browzer/<feat>/workflow.json`. Every mutator acquires an advisory flock, validates the post-mutation shape against the CUE schema, and writes atomically (tmp + rename + optional fsync).

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
make mutate          # writes packages/cli/mutate-out/report.txt
```

### CUE schema codegen (`schemas/`)

`schemas/workflow-v1.cue` is the SSOT for `workflow.json`. Everything downstream is generated. **Never** hand-edit `workflow-v1.schema.json`, `cue_types_workflow_gen.go`, the embed mirrors under `internal/schema/`, or `packages/skills/references/workflow-schema.md`.

```bash
make -C schemas all          # regenerate every artifact (after editing the .cue)
make -C schemas ci-check     # assert no drift between checked-in artifacts and fresh codegen
make -C schemas vet          # cue vet schema + valid/invalid fixtures
make -C schemas clean        # remove generated artifacts

# After codegen, mirror to per-skill copies (run from monorepo root):
node packages/skills/scripts/sync-shared-refs.mjs
node packages/skills/scripts/sync-shared-refs.mjs --check    # CI drift gate
```

`make ci` calls `make -C schemas ci-check` as step 1b — codegen drift fails CI before vet/test/cross-compile run.

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
   ├── (scripts/cue-to-markdown.mjs)                                          →  packages/skills/references/workflow-schema.md
   └── (cp)                                                                   →  internal/schema/workflow-v1.cue (embed mirror)
```

Why two enrichment passes on the JSON Schema:

1. Carry `time.Format(time.RFC3339)` through as `format: "date-time"` so `scaffold.go` can emit RFC3339 placeholders rather than empty strings (otherwise scaffolds fail CUE validation immediately).
2. Align `*default | T` fields with the WF-OPTIONAL-MARKER contract — fields with explicit defaults are not required from operator POV, even though OpenAPI projection marks them required.

`internal/schema/describe.go` honours `cue.Iterator.IsOptional()` (the `?` marker) — audited from the skills side by `scripts/audit/cli-describe-step-type-respects-optional-marker.mjs`.

Schema v2 is the current contract (since 2026-05-04). Workflows tagged `schemaVersion: 1` are read-only legacy; mutator verbs reject writes to them.

### Daemon vs standalone write modes

Every workflow mutator honours three execution modes:

| Mode      | Path                       | Cost                                          |
| --------- | -------------------------- | --------------------------------------------- |
| `--sync`  | in-process standalone      | ~500ms (full read/validate/write)             |
| `--async` | daemon FIFO (default)      | <50ms — daemon completes durably in background |
| `--await` | daemon + fsync wait        | ~50–120ms — durable when the call returns     |

Override via `BROWZER_WORKFLOW_MODE=sync|async|await`. CI/test paths force standalone (`--sync`) when a long-running daemon binary may be stale.

The daemon (`internal/daemon/`) speaks JSON-RPC over a Unix socket, hosts the read-filter pipeline (AST trimming for `browzer read --filter auto`), the manifest + session caches, and the `workflow_queue` for `--async` writes. Lifecycle: `browzer daemon start --background`, `browzer daemon status --json`, `browzer daemon stop`.

### LLM/agent-friendly conventions

Every read command supports:

- `--json` — compact JSON to stdout, no banners.
- `--save <file>` — write JSON to a file (implies `--json`, stdout silent — receipts pattern used heavily by skills).
- `--schema` — print the response JSON Schema without hitting the server (discovery without payload).
- `--ultra` — ultra-compact output (smaller payloads, fewer fields).
- `--llm` — LLM mode: suppresses banners, disables colors + spinners.
- `-v`/`-vv`/`-vvv` — verbosity (decisions / subprocess / raw I/O).

Mutators add `--quiet` (default-on under `BROWZER_LLM=1` for all 16 mutators via `quietByDefaultUnderLLM()` at `internal/commands/workflow_mutator_helpers.go:47`).

`describe-step-type` accepts `--include-base` (merge base step fields), `--field <jq-path>` (project a sub-tree), and `--save <path>` (cache the schema slice; skills use `/tmp/<feat>/.schema-cache/<NAME>.json`).

### `--render` templates

`browzer workflow get-step <id> --render <template>` emits compressed prompt-embed projections. The `.jq` renderer files live on the skills side under `packages/skills/scripts/renderers/` (9 files: `brainstorm`, `code-review`, `commit`, `feature-acceptance`, `prd`, `receiving-code-review`, `task`, `tasks-manifest`, `update-docs`). One additional template — `task-agent` — is implemented natively in Go at `internal/workflow/render.go:31`. When adding/renaming a template here, update both sides plus `scripts/audit/render-coverage.mjs` on the skills side.

### Named queries

`browzer workflow query <name>` is the only allowed mechanism for cross-step aggregations. Current catalogue (10): `reused-gates`, `failed-findings`, `open-deferred-actions`, `task-gates-baseline`, `changed-files`, `deferred-scope-adjustments`, `open-findings`, `next-step-id`, `cache-warm-deps`, `cache-warm-mentions`. Skills side enforces "no unknown named query" via audit. New queries land here AND in the catalogued list — never in skill-side `jq`.

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
- **CUE edits → run `make -C schemas all`** before committing. CI's `make ci-check` will fail otherwise. Then run the skills-side `sync-shared-refs.mjs` to mirror the markdown into per-skill references.
- **Adding a new mutator verb**: wire it under `internal/commands/workflow_*.go`, register validation against the post-mutation CUE shape, default `--quiet` on under `BROWZER_LLM=1`, and document it in the README's "Workflow state" table. Skills cannot consume it until skill bodies + `skill-cli-references.mjs` audit allowlists are updated in the same change.
- **Adding a CLI flag visible to skills**: update the README, audit `skill-cobra-arg-syntax.mjs` examples, and rerun `skill-cli-sync-drift.mjs`.
- **JSON output stability**: every read command's `--json` payload is consumed by skills as a contract. Field renames are breaking changes; gate behind a major bump (cli-v2.0.0 was the last BREAKING — `#TestSpec.type → intent`, Explorer rich→lean projection, `--quiet` parity on `explore`/`search`).
- **Banners go to stderr** (regression-pin test exists). Stdout is reserved for command output.
- **JSON encoding**: read paths that re-encode workflow JSON use `marshalNoHTMLEscape` (`json.Encoder` with `SetEscapeHTML(false)`) — needed because earlier versions hit a parse race on PRDs ~35 KB. There's a 50-iter regression test (`TestWorkflowQuery_StepsByName_AfterRecentAppendStep_ProducesValidJSON`); keep it green.
- **Daemon detach**: platform-split between `daemon_detach_unix.go` and `daemon_detach_windows.go`. Don't merge.
