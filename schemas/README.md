# `packages/cli/schemas/`

Single source of truth for `docs/browzer/<feat>/workflow.json` (schema v2).

Edit `workflow-v1.cue` only. Everything else under this directory is
**generated** by `make all` — hand-edits are reverted by `make ci-check` in
CI. The markdown reference that used to be generated alongside
(`packages/skills/references/workflow-schema.md`) was retired in CLI v3.0.0;
skills now discover shapes at runtime via
`browzer workflow describe-step-type <NAME> --json`.

## Layout

```
packages/cli/schemas/
├── workflow-v1.cue              # SSOT — hand-edited
├── workflow-v1.schema.json      # generated (OpenAPI 3.0 projection)
├── cue_types_workflow_gen.go    # generated (Go structs, package=workflow)
├── Makefile                     # codegen + ci-check + vet
├── fixtures/
│   ├── valid/*.json             # 6 fixtures that MUST validate
│   └── invalid/*.json           # 10 fixtures that MUST be rejected
└── README.md                    # this file
```

## Codegen pipeline

```
                 workflow-v1.cue (hand-edited SSOT)
                            │
                            ▼
           ┌────────────────┴────────────────┐
           ▼                ▼                ▼
    cue def --out      cue exp gengotypes  cp embed mirrors
    openapi             .                  (internal/schema/)
           │                │
           ▼                ▼
    workflow-v1.    cue_types_workflow_
    schema.json     gen.go
```

`make all` regenerates every artifact. `make ci-check` asserts no drift
between the checked-in artifacts and a fresh codegen — fatal in CI.

## How to edit the SSOT

1. Open `workflow-v1.cue`.
2. Add or modify fields. Every leaf field MUST carry an `@addedIn("<ISO>")`
   attribute — the date its enforcement starts. Without `@addedIn` the
   field is invisible to `browzer workflow validate --since-version`.
3. Run `make all` — regenerates JSON Schema + Go + embed mirrors.
4. Run `make ci-check` — asserts everything is in sync.
5. If you added a new mandatory field, add a fixture under
   `fixtures/invalid/` that demonstrates the missing-field diagnostic.
6. If you added a new step type, add a fixture under `fixtures/valid/`.

## How codegen works

| Tool | Input | Output |
|---|---|---|
| `cue def --out openapi` | `workflow-v1.cue` | `workflow-v1.schema.json` (OpenAPI 3.0) |
| `cue exp gengotypes` | `workflow-v1.cue` | `cue_types_workflow_gen.go` (Go structs) |
| `cp` (embed mirror) | `workflow-v1.{cue,schema.json}` | `internal/schema/workflow-v1.{cue,schema.json}` |

`cue` v0.15.0+ is required (`go install cuelang.org/go/cmd/cue@v0.15.0`).
The Go generator output ships with the package so consumers don't need
the CUE binary at build time — only when re-generating after a schema
edit.

## How to add a new field

```cue
// In workflow-v1.cue, inside the appropriate definition struct:
newField: string @addedIn("2026-05-04T00:00:00Z")
```

After saving:

```bash
cd packages/cli/schemas
make all          # regenerate JSON Schema + Go + embed mirrors
make ci-check     # verify everything is in sync
```

Then add an invalid fixture demonstrating the missing-field case:

```bash
# fixtures/invalid/missing-new-field.json
# (a workflow.json that omits newField — should be rejected by `cue vet`)
```

## How to add a new step type

1. Add `#FooStep: #StepBase & { name: "FOO", foo: #FooPayload }` in
   `workflow-v1.cue`.
2. Define `#FooPayload: { ... }` with each leaf field carrying
   `@addedIn`.
3. Add `FOO: #FooStep` to the `#StepDefinitions` registry.
4. Add `"FOO"` to the `#StepName` disjunction.
5. Add `#FooStep` to the `#Step` discriminator union.
6. Add a fixture under `fixtures/valid/foo-workflow.json`.
7. `make ci-check`.

## Backward-compat policy

Per `docs/WORKFLOW_SYNC_REDESIGN.md` Q3 — **hard cutoff**, no parallel-version
tolerance:

- `schemaVersion: 2` is the only accepted top-level version.
- Existing workflows under `schemaVersion: 1` become permanently
  read-only post-merge. No automatic migration script.
- The `--since-version` flag on `browzer workflow validate` (when the
  CLI gains it via `WF-CLI-VALIDATE-1`) lets the judge skill compare
  against an earlier `@addedIn` cutoff for retro-judging old runs that
  predate this PR — but actively-running workflows always validate
  against the latest version.

If you need to make a backwards-incompatible change post-merge, bump
`schemaVersion: 3` and document the migration in
`docs/WORKFLOW_SYNC_REDESIGN.md`.

## CI gates

- `cd packages/cli/schemas && make ci-check` — fatal in `quality` job.
- `cue vet workflow-v1.cue` — runs in <100ms locally; <200ms is the NFR.
- All `fixtures/valid/*.json` validate via `cue vet -E -c -d '#WorkflowV1'`.
- All `fixtures/invalid/*.json` produce diagnostics (non-zero exit).

## Local installation

```bash
go install cuelang.org/go/cmd/cue@v0.15.0
cd packages/cli/schemas && make ci-check  # should exit 0
```
