# packages/cli — CLAUDE.md

Browzer CLI. **Written in Go, not Node.** Read the root `CLAUDE.md` first.

## Not part of the pnpm monorepo

- No `package.json`, no `node_modules`, does NOT participate in `pnpm turbo lint typecheck test`.
- Has its own `go.mod` (module `github.com/browzeremb/browzer-cli`, Go 1.25+) and goreleaser pipeline.
- Run its tests with `cd packages/cli && go test ./...`.
- Build locally with `cd packages/cli && go build -o "$HOME/.local/bin/browzer" ./cmd/browzer`.
- Released as `v1.3.3` (current — aligned with skills package v3.2.3; carries the workflow-skill contract tightening: subagent loop-escape + partial-status emission, code-review mutation-runner probe + agent-teams round-table dispatch, feature-acceptance scopeAdjustments inheritance, update-docs citation policy + CHANGELOG-with-hash rule, workflow-schema completedAt bump invariant, commit unconditional co-author trailer — no public CLI behavior change). v1.3.2 (predecessor) was the harden-only release adding daemon `TestDaemonLifecycle_StartCacheHitMissStop` integration test plus new `tracker_test.go` and `telemetry/batcher_test.go`. v1.3.1 (predecessor) and v1.3.0 also added `--workspace-ids` flag on `ask`, `search`, `explore`, `deps` (`internal/flags/workspaces.go`) for cross-workspace queries, and `internal/format/score.go` normalizes `score` via `(2/π)·atan(raw)` — the same arctan transform now used by the TS pipeline, so scores are comparable across surfaces. v1.3.1 + v1.3.0 (2026-04-27) ship the `browzer mentions` graph traversal command (`File ← RELEVANT_TO ← Entity ← MENTIONS ← Chunk ← HAS_CHUNK ← Document`) consumed by the `update-docs` skill, plus the `--anchors` flag on `explore` with a stable `anchor` field, and a `staleness` block in `status --json`. v1.0.16 (2026-04-20 sessão 2) ported the `ec37933` defer-headline + exit-code-7/8 pattern from `workspace docs --add` into `workspace sync` and added `--no-wait` to sync. v0.8.0 shipped the token-economy subsystem (daemon + tracker + telemetry + hooks + `read`/`gain`/`plugin`/`config` commands); v1.0.0 was the first stable release and shipped the public marketplace-based plugin flow (`/plugin marketplace add browzeremb/skills`) — the old file-drop `browzer plugin install` is now a printer of marketplace instructions. v1.0.3 calibrated the `savedTokens` formula per-language against Claude 4 via `count_tokens` — mean absolute error on the reported savings dropped from 35% to 14%.

## Canonical reconciliation command

`browzer sync` is the single canonical command to bring the server-side
index into match with the local working tree. It re-indexes code
structure AND reconciles documents (ADD + UPDATE + DELETE) driven by
`.gitignore ∩ .browzerignore`. Partial runs: `--skip-code` or
`--skip-docs`. For the legacy interactive huh TUI, use
`browzer workspace docs --interactive`.

Non-interactive curation (scripting / CI / agents) stays on
`browzer workspace docs --add <spec>` / `--remove <spec>` /
`--spec <file>` exactly as before RAG-UX-1 — those flag paths did not
change.

`browzer workspace index` (no flags) is a thin alias for
`browzer sync --skip-docs`; `browzer workspace docs` (no flags) is a
thin alias for `browzer sync --skip-code`. The legacy `workspace docs`
default-flow TUI is no longer the default — pass `--interactive` to
open it explicitly.

## Local verification (REQUIRED before pushing CLI changes)

The CLI's CI runs **four independent checks** that a plain `go test ./...` does NOT cover: `go vet`, `go test -race`, cross-compile for 5 targets (darwin/linux arm64+amd64, windows/amd64), and `golangci-lint v2.5.0`. Each of these has blocked past CI runs because the dev cycle never exercised them locally.

**Always run `make ci` before pushing** — it mirrors the public `browzeremb/browzer-cli` CI exactly:

```bash
cd packages/cli && make ci
```

On first run the script auto-installs `golangci-lint v2.5.0` into `$(go env GOPATH)/bin`. If `make ci` passes locally, `.github/workflows/ci.yml` on the public repo will pass too (same commands, same versions). Skipping this step is how you end up pushing a commit that only reveals its problem once it hits the remote runner. The script itself is at `packages/cli/scripts/ci-local.sh` and is the source of truth; the `Makefile` target is just an ergonomic entry point.

The monorepo CI has a `cli-ci` job that runs the same script, and the `mirror-cli.yml` workflow only fires via `workflow_run` after CI succeeds — so a commit that would break the public CLI can't reach the public repo in the first place. The `make ci` gate is the fast pre-push check; the monorepo `cli-ci` job is the last-resort fallback.

## Cross-platform discipline

`cmd/browzer` is built for 5 GOOS/GOARCH combos. Anything Unix-specific (`syscall.SysProcAttr.Setsid`, `os.Getuid()`-derived paths, `/tmp` hardcoding, `unix.*`) MUST be isolated behind `//go:build !windows` / `//go:build windows` file pairs. Pattern: helper file `foo_unix.go` (`//go:build !windows`) exports the function; `foo_windows.go` (`//go:build windows`) provides a no-op or Windows-equivalent stub. Example: `internal/commands/daemon_detach_unix.go` / `daemon_detach_windows.go`. `make ci` catches violations via the windows cross-compile step.

The daemon subsystem (Unix socket, uid-derived paths) is structurally Unix-first — Windows builds link but the daemon is not a usable runtime there (see Known limitations in README). Don't add new daemon features behind Windows build tags unless you implement the Windows-native equivalent (named pipes + `CREATE_NEW_PROCESS_GROUP`); a no-op stub is fine for anything that currently falls back to Unix-only behavior.

## Shape

- `cmd/browzer/` — entrypoint (cobra root + subcommand wiring).
- `internal/commands/` — one file per subcommand (48 files as of v0.8.0). `root.go` is the single source of truth for which commands exist.
- `internal/api/` — HTTP client against `apps/api` + `apps/auth`.
- `internal/auth/` — device flow client, token storage (`~/.browzer/credentials`), `Credentials.TelemetryConsentAt` LGPD consent timestamp populated on `login` via `/api/auth/me`.
- `internal/config/` — `env.go` (`DefaultServer` honors `BROWZER_SERVER` env var), `keys.go` (socket path, PID path, history DB path — all uid-derived), `config.go` (persisted settings in `~/.browzer/config.json`).
- `internal/daemon/` — Unix-socket JSON-RPC server. `server.go` (accept loop, method dispatch), `client.go` (RPC caller), `filter.go` (AST rewriter: minimal/aggressive/auto — `auto` uses manifest), `manifest_cache.go` (workspace manifest cache, reads `.browzer/manifest.json` lazy), `session_cache.go` (extracts model from transcript jsonl on SessionRegister). Post-2026-04-28 (feat-20260428-web-dashboard-improvements): `server.go` exposes a cumulative org-scoped `tokensEconomized` counter via the daemon's HTTP surface, consumed by the dashboard KPI card through `apps/api`'s `GET /api/telemetry/tokens-economized`. Counter resets on daemon restart by design.
- `internal/cache/manifest.go` (NEW 2026-04-28) — file-backed `WorkspaceManifest` for tracking known workspaces between `browzer sync` runs, JSON at `os.UserCacheDir()/browzer/workspace-manifest.json` keyed by orgId. Supports last-writer-wins reconciliation when the dashboard mutates a workspace remotely (the `workspace_sync.go` reconciliation extension issues `client.UpdateWorkspace` / `client.DeleteWorkspace` against the apps/api workspace CRUD routes for entries marked `locallyModified`).
- `internal/tracker/` — SQLite history DB (`modernc.org/sqlite`, pure Go, no CGO). `Record(Event)` for daemon-side writes, `UnsentBuckets()` + `MarkFlushed()` for the batcher, `Cleanup()` 90-day retention.
- `internal/telemetry/` — `batcher.go` (periodic flush of unsent buckets), `sender.go` (POSTs to `${server}/api/telemetry/usage` with `Authorization: Bearer`). Consent-gated by `consentGatedSend` wrapper in `daemon_cmd.go` — short-circuits to no-op when `creds.TelemetryConsentAt == nil`.
- `internal/walker/` — filesystem walker with gitignore + `isSensitive` filtering.
- `internal/upload/` — multipart upload helpers.
- `internal/urlvalidate/`, `internal/git/` (includes `RealPath` — macOS case-insensitive path canonicalization), `internal/cache/`, `internal/output/`, `internal/ui/`, `internal/errors/`, `internal/prompts/`, `internal/schema/` — support packages.

## Subsystems (v0.8.0 token-economy umbrella)

The token-economy feature set is a single spec (delivery log at `docs/CHANGELOG.md §2026-04-15 "CLI token economy"`; original detailed spec archived in git history) — implemented across four subsystems that MUST stay decoupled:

1. **Tracker** (`internal/tracker/`): SQLite store at `~/.browzer/history.db`. Append-only `events` table, one row per tool invocation. Used by `gain` for aggregation, by the daemon's `Track` RPC for writes, and by the batcher for flush.
2. **Daemon** (`internal/daemon/`): Unix-socket JSON-RPC server. Serves `Read`, `Track`, `SessionRegister`, `Health`, `Shutdown`. Idle-watches itself and exits after `daemon.idle_timeout_seconds` of no traffic. Started manually (`daemon start --background`) or via the plugin's SessionStart hook.
3. **Telemetry** (`internal/telemetry/`): Batcher + sender. Flushes unsent tracker rows to `POST /api/telemetry/usage` every 5 min. Consent-gated — if `creds.TelemetryConsentAt == nil`, the batcher runs but `send()` is a no-op.
4. **Hooks + plugin** (`packages/skills/hooks/guards/*.mjs`): PreToolUse hooks that hit the daemon via client RPC to rewrite `Read`/`Glob`/`Grep`/`Bash` tool_inputs. The plugin is installed **from inside Claude Code** via `/plugin marketplace add browzeremb/skills` + `/plugin install browzer@browzer-marketplace` (the public mirror of `packages/skills/` maintained by `.github/workflows/mirror-skills.yml`). An older `browzer plugin install` command copied files into `.claude/plugins/browzer/` — Claude Code does not auto-discover plugins from that path, so the command is now a printer of marketplace instructions (`browzer plugin`).

Subsystem isolation matters: a broken daemon MUST NOT break `browzer search`; a broken batcher MUST NOT lose tracker data (batcher reads from the DB, it doesn't own it); a broken telemetry sender MUST NOT block `read` (track is fire-and-forget from the hook's POV).

## Release flow

1. `git tag cli-v<semver> && git push origin cli-v<semver>` in this monorepo.
2. `.github/workflows/mirror-cli.yml` mirrors source to public `github.com/browzeremb/browzer-cli` and creates a stripped `v<semver>` tag there. Main-branch pushes fire via `workflow_run` gated on the monorepo `CI` workflow (incl. `cli-ci` job) succeeding; tags fire via direct `push` trigger because tag pushes don't re-run main-branch CI.
3. The public repo's `release.yml` runs goreleaser → GitHub Releases + `browzeremb/homebrew-tap` (cask) + `browzeremb/scoop-bucket` (manifest).
4. Watch the public-side run with `gh run watch <id> --repo browzeremb/browzer-cli`. Verify with `gh release view v<semver> --repo browzeremb/browzer-cli` — confirm `prerelease: false` for stable cuts.

### Secrets

- `MIRROR_SSH_PRIVATE_KEY` (this repo) — matches a write-enabled deploy key on `browzer-cli`.
- `HOMEBREW_TAP_TOKEN` (on the public `browzer-cli` repo) — fine-grained PAT with Contents:write on all three release repos (`browzer-cli`, `homebrew-tap`, `scoop-bucket`).

## Install script

`packages/cli/install.sh` is the source of `https://browzeremb.com/install.sh`. That URL is a 302 redirect configured in `apps/web/next.config.ts` → raw `install.sh` on the public mirror.

## Wire-format compatibility

The Go CLI replaced an earlier Node CLI. **Wire format (HTTP routes, JSON shapes, exit codes, file formats) is byte-compatible** with the Node version — changing any of these requires a coordinated server change.

### Server endpoints the CLI depends on

| Endpoint | Used by | Since |
| --- | --- | --- |
| `POST /api/auth/api-key/verify` (via `apps/api`) | every authenticated call | v0.1.0 |
| `GET /api/auth/me` | `login` (populates `TelemetryConsentAt`) | v0.8.0 |
| `GET /api/workspaces` + `/:id/*` | workspace commands | v0.1.0 |
| `GET /api/workspaces/:id/explore` | `explore` (adds `exports`, `imports`, `importedBy`, `lines`, `score`, `type`) | v0.5.0 |
| `GET /api/workspaces/:id/deps` | `deps` — flags `--reverse`, `--limit`, `--json`, `--save`, `--schema` | v0.6.0 |
| `POST /api/ask` | `ask` — 3-tier `workspaceId` fallback, never sends empty; supports `--workspace-ids` flag for cross-workspace | v0.6.0 |
| `POST /api/workspaces/ask` | `ask --workspace-ids id1,id2` — cross-workspace ask (§16) | v1.3.0 |
| `POST /api/workspaces/search` | `search --workspace-ids id1,id2` — cross-workspace search (§16) | v1.3.0 |
| `POST /api/telemetry/usage` | daemon telemetry batcher | v0.8.0 |

Older CLI versions ignore newer response fields (Go decoder drops unknown keys). New response fields can ship CLI-first; new request fields require CLI + server coordination.

### macOS case-sensitivity

`git.RealPath(path)` in `internal/git/git.go` resolves paths to their canonical filesystem casing by walking each component via `os.ReadDir`. Use this before `filepath.Rel(gitRoot, abs)` to avoid mismatches between `os.Getwd` (may return `desktop`) and git (returns `Desktop`). `FindGitRoot` applies it automatically.

## Auth

- `browzer login` triggers the device flow against `apps/auth`. Credentials land in `~/.browzer/credentials` as JSON keyed by profile name (default: `default`). As of v0.8.0 the payload also includes `TelemetryConsentAt *string` — populated from `GET /api/auth/me`, used to gate the telemetry batcher.
- Smoke-test bearer: `jq -r .default.access_token ~/.browzer/credentials`.
- `PollForToken` accepts a `Clock` interface (`internal/auth/clock.go`). Production callers pass `auth.RealClock{}`; tests inject `FakeClock` (defined in `device_flow_test.go`) to advance virtual time via `Advance(d)` — zero real sleeps, suite runs in <5s.

## Config persistence

`~/.browzer/config.json` holds user-settable keys managed by `browzer config`. Known keys:

| Key | Default | Meaning |
| --- | --- | --- |
| `tracking` | `on` | Whether the daemon records to SQLite |
| `hook` | `on` | Whether Claude Code hooks in the plugin are active |
| `telemetry` | `on` (gated by consent) | Whether the batcher flushes to the server |
| `daemon.idle_timeout_seconds` | `900` | How long the daemon waits before self-exit |
| `daemon.socket_path` | auto (`/tmp/browzer-daemon.<uid>.sock`) | Override for tests |

When a key is absent, `isHookEnabled()` / `isTrackingEnabled()` / `isTelemetryEnabled()` return `true` — defaults are "on". Setting `off` persists explicit opt-out.
