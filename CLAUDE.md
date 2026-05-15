# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Authoritative references (read first)

- **`docs/CLAUDE_CODE_PLUGIN.md`** — single source of truth for the broader Browzer plugin/CLI consolidation: layout, invariants, CI gates, version trajectory, session-log fixes. Always consult before any non-trivial change here.
- **`packages/skills/`** — the Claude Code plugin that consumes this CLI. Skills are markdown wrappers around `browzer` subcommands. This CLI MUST stay synchronized with that plugin: any change to a subcommand surface (flags, JSON shape, exit codes) requires patching every skill body that invokes it in the same change. Skill-side audits gate that drift in the monorepo `quality` job. See `packages/skills/CLAUDE.md`.

## What this package is

`@browzer/cli` is a single-binary Go CLI (`browzer`, current version `5.0.0` — `VERSION`, requires Go **≥ 1.25**) that talks to the Browzer server over HTTPS and to a local Unix-socket daemon over JSON-RPC. It is the runtime side of the Browzer plugin: skills are thin markdown contracts; this CLI is what they actually invoke.

Compatibility: CLI v5.x.x ↔ Plugin v5.x.x ↔ Tracker schema v2

Single surface — **RAG / token-economy**: `login`, `workspace {init,sync,index,docs,list,relink,unlink,files-list,docs-list,...}`, `explore`, `search`, `deps`, `mentions`, `read`, `gain`, `daemon`, `ask`, `job`, `org`, `codereview aggregate`. The `read`/`glob`/`grep` rewrites driven by the plugin's hooks land here. The legacy `workflow.json` mechanics (`save-step`, `get-step`, `save-step-batch`, every `workflow <verb>` subcommand, the CUE codegen pipeline, the daemon `WorkflowMutate` RPC) were retired in v5.0.0 — see `CHANGELOG.md` v5.0.0 for the migration note pointing at the markdown-chains pipeline.

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
go test -race ./internal/commands/...
go test -race -run TestExploreEndpoint ./internal/api/...
```

## Architecture — big picture

### Layered composition

```
cmd/browzer/main.go
       │
       ▼
internal/commands/        ← Cobra command tree
       │
       ├── internal/api/         HTTPS client to the Browzer server (workspaces, search, ask, jobs, billing, org, github_releases, preflight)
       ├── internal/auth/        Device-flow OAuth + API-key login + revoke; credentials at ~/.browzer/credentials
       ├── internal/daemon/      Unix-socket JSON-RPC daemon: filter pipeline, manifest cache, session cache
       ├── internal/schema/      Per-command JSON-schema constants + Print / PrintOrSave helpers consumed by the `--schema` flag on every `workspace_*` / `org_*` verb
       ├── internal/tracker/     SQLite tracker (savedTokens, per-language coefficients) for `browzer gain`
       ├── internal/cache/       Manifest + session caches consumed by the daemon
       ├── internal/format/      JSON / table / ultra / llm output rendering
       ├── internal/flags/       Shared cobra flag wiring (--json, --save, --ultra, --llm, -v..-vvv, --schema)
       ├── internal/config/      ~/.browzer/config.json + .browzer/config.json (workspace) + .browzer/skills.config.json
       └── internal/{walker,git,upload,urlvalidate,ui,errors,version,output,telemetry,codereview}
```

`cmd/browzer/main.go` is a thin entrypoint; everything testable lives in `internal/commands/` and the supporting packages.

### Daemon JSON-RPC surface

The daemon (`internal/daemon/`) speaks JSON-RPC over a Unix socket and serves three functional method handlers — `Read` (with the read-filter pipeline / AST trimming for `browzer read --filter auto`), `Track` (telemetry events), `SessionRegister` (binds a Claude Code session to a workspace) — plus the housekeeping handlers `Health`, `Daemon.Version`, `Shutdown`, and `TokensEconomized`. `CurrentProtocolVersion = 3`. Capabilities advertised: `read.v1`, `track.v1`, `session-register.v1`. The manifest + session caches live alongside.

Lifecycle: `browzer daemon start --background`, `browzer daemon status --json`, `browzer daemon stop`.

### LLM/agent-friendly conventions

Every read command supports:

- `--json` — compact JSON to stdout, no banners.
- `--save <file>` — write JSON to a file (implies `--json`, stdout silent — receipts pattern used heavily by skills).
- `--schema` — print the response JSON Schema without hitting the server (discovery without payload). Served by the preserved `internal/schema/schema.go` surface.
- `--ultra` — ultra-compact output (smaller payloads, fewer fields).
- `--llm` — LLM mode: suppresses banners, disables colors + spinners.
- `-v`/`-vv`/`-vvv` — verbosity (decisions / subprocess / raw I/O).

`--quiet` is wired cross-cutting on the read verbs `status`, `explore`, `search`, `deps`, `mentions`, and `workspace sync` via the shared `output.RegisterQuietFlag()` helper in `internal/output/verbosity.go` so skill bodies can suppress decorative output uniformly.

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
- **Adding a CLI flag visible to skills**: update the README, run the skill-side audits, and rerun `skill-cli-sync-drift.mjs` from `packages/skills/`.
- **Plugin host-agnosticism**: a pre-push audit `audit-skill-host-agnosticism` ensures `packages/skills/` contains no monorepo-specific paths (e.g. `packages/cli/internal/`, `apps/api/`) in SKILL.md or hook prose. The plugin is mirrored to `browzeremb/skills`; this audit is mandatory before any PR that touches skill bodies or hooks.
- **JSON output stability**: every read command's `--json` payload is consumed by skills as a contract. Field renames are breaking changes; gate behind a major bump.
- **Banners go to stderr** (regression-pin test exists). Stdout is reserved for command output.
- **Daemon detach**: platform-split between `daemon_detach_unix.go` and `daemon_detach_windows.go`. Don't merge.
