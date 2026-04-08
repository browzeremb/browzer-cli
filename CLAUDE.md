# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Role

`@browzer/cli` — Browzer CLI written in **Go**. Single static binary,
no Node, no CGO. Talks to `apps/api` (and `apps/auth` for the device
flow). Source of truth lives at `packages/cli/` in the private monorepo
and is mirrored to `github.com/browzeremb/browzer-cli` (public) by the
`mirror-cli` workflow.

## Stack

- **Go 1.21+**, no CGO. Stdlib `net/http`, `mime/multipart`,
  `crypto/sha256`, `os/exec` cover 90% of the surface.
- CLI framework: `github.com/spf13/cobra`.
- Gitignore: `github.com/sabhiram/go-gitignore`.
- Interactive prompts: `github.com/charmbracelet/huh` (TTY-aware).
- Browser launch: `github.com/pkg/browser`.
- TTY detection: `golang.org/x/term.IsTerminal`.

## Commands

From `packages/cli/`:

- Build local binary: `go build -o /tmp/browzer ./cmd/browzer`
- Run tests: `go test ./...`
- Vet: `go vet ./...`
- Cross-compile sanity: `GOOS=linux GOARCH=arm64 go build -o /dev/null ./cmd/browzer`
- Goreleaser dry-run: `goreleaser release --snapshot --clean --skip=publish`

## Layout

```
cmd/browzer/main.go             entrypoint: signal handlers + cobra root
internal/
├── commands/                   one file per command, mirrors src/commands/*.ts
├── api/                        HTTP client + DTOs (one file per route group)
├── auth/                       credentials + RFC 8628 device flow
├── config/                     env overrides + .browzer/config.json
├── walker/                     code + docs walker, sensitive filter, gitignore
├── cache/                      .browzer/.cache/docs.json
├── upload/                     multipart batch upload pipeline
├── git/                        findGitRoot, checkStaleness via shell-out
├── output/                     emit (--json/--save), formatters, exit codes
├── errors/                     CliError + exit-code constants
└── urlvalidate/                server URL safety check
```

## Command surface

Canonical form is **noun-grouped**: `browzer workspace {init,sync,
status,explore,search}`. The legacy top-level aliases (`browzer init`,
`browzer sync`, etc.) are still registered for backward compat. Both
forms call the same handlers via the dual-registration block in
`internal/commands/root.go`.

## Conventions

- **Every read/search command supports `--json` and `--save <file>`.**
  Output routing lives in `internal/output/emit.go`. JSON is compact
  (no indent) so `jq` and python parsers are happy.
- **`output.Errf` writes to stderr.** Never use `fmt.Println` for
  warnings — they would interleave with `--json` stdout and break SKILL
  parsers.
- **Walker invariants are sacred** (`internal/walker/`):
  - `IsSensitive` checked **BEFORE** any `os.Stat`/`os.Open` syscall.
  - Symlinks (file or dir) skipped at every level.
  - `MaxDepth = 32` cap on recursion.
  - `IsBinaryFile` probes first 512 bytes for null/non-printable.
  - `DefaultIgnoreDirs` (node_modules, dist, ...) always excluded.
- **`BROWZER_HOME` honored everywhere.** Tests must call
  `t.Setenv("BROWZER_HOME", t.TempDir())` to isolate from the
  developer's real `~/.browzer/`.
- **Atomic writes**: credentials, cache, config all use temp file +
  `os.Rename`. Never write in place.
- **Cold-start timeout**: `init` and `sync` set `requireAuth(600)` (600 s)
  because the first call against a fresh server cold-starts the
  embedding model. All other commands use the 30 s default.
- **Multipart `paths` field**: every batch upload sends a parallel
  `paths` JSON array because Fastify multipart strips path components
  from `part.filename`. Without it, doc names lose directory context.
- **Async pollers always honor `If-None-Match` (ETag 304)**. See
  `internal/api/jobs.go:PollBatchStatus`.

## Distribution

The `mirror-cli` workflow in the **private monorepo** does
`git subtree split` on every push that touches `packages/cli/**` and
mirrors the result to `github.com/browzeremb/browzer-cli` (public). On
tag push (`cli-v*`), it propagates the tag as `v*` on the public side.

The **public repo** runs:

- `release.yml` — `goreleaser release --clean` on `v*` tag → publishes
  Release tarballs, updates the Homebrew tap, updates the Scoop bucket.
- `ci.yml` — `go test`, `go vet`, golangci-lint, full cross-compile
  matrix on every push.

Distribution channels (all configured in `.goreleaser.yaml`):

1. `curl -fsSL https://browzeremb.com/install.sh | sh`
2. `brew install browzeremb/tap/browzer`
3. `scoop install browzer`
4. `go install github.com/browzeremb/browzer-cli/cmd/browzer@latest`

## Gotchas

- **`go install @latest` requires a valid semver tag.** Use `v0.1.0`,
  `v0.1.1`, ... — not `v0.1.0-rc.1` if you want it to be the default
  for `@latest`.
- **`huh.NewConfirm` blocks on non-TTY.** Always check `isTTY()` before
  calling it. The non-TTY branch must have a sensible default.
- **`exec.Command("git", ...)`** inherits the current working dir
  unless `cmd.Dir` is set. `git.FindGitRoot` always sets it.
- **`go-gitignore` does not have an incremental `Add` API.** The
  walker's `ignoreMatcher` accumulates lines in a flat slice and
  recompiles **lazily** (dirty flag) on the next `matches()` call.
  The cost is still `O(N²)` in pathological repos with hundreds of
  nested gitignores, but lazy compilation removes the redundant
  per-`add()` rebuilds inside an add-burst.
- **DO NOT replace the flat list with a per-frame stack.**
  `go-gitignore`'s negation only flips an already-positive match
  within the SAME compiled set, so a child `.gitignore`'s
  `!important.log` cannot undo a parent's `*.log` ignore from a
  separate frame. `TestWalkRepo_NestedGitignoreStack` pins this case
  exactly — do not "optimize" it away.
- **`walker.IsSensitive` normalizes paths twice** —
  `filepath.ToSlash` (no-op on POSIX) AND an explicit
  `strings.ReplaceAll(\\, /)` to catch literal backslashes that
  travel cross-OS in test fixtures or wire payloads. Drop either
  and the Windows-style `src\config\.env` test in
  `sensitive_test.go` fails.
- **`browzer login --server <url> --key dummy`** is the canonical
  way to exercise `urlvalidate.Validate` without standing up a
  server — validation runs before any network call, so reject
  cases exit fast with the rejection message.
- **`go test ./...` is slow (~75 s) because of `internal/auth`** —
  `device_flow_test.go` exercises `PollForToken`'s real `time.After`
  sleeps (interval is clamped to `minIntervalSeconds=5`). When
  iterating on a different package, scope with
  `go test ./internal/<pkg>/...`.
- **`BROWZER_SERVER` lives in `internal/config`, not `internal/auth`.**
  Importing config from auth is fine; the reverse would create a cycle.
- **Local CLI regression test**: `.claude/skills/browzer-cli-regression-test/SKILL.md`
  is the runbook. Run before AND after any change to `packages/cli/`,
  `apps/api/src/routes/`, `apps/worker/src/`, `apps/auth/src/`,
  `apps/gateway/src/`, or `docker-compose.yml`. Catches regressions
  unit tests miss.

## Release + goreleaser gotchas

- **Cut a release**: `git tag cli-v0.1.0 && git push origin cli-v0.1.0` in the **private monorepo**. The `mirror-cli` workflow strips `cli-` and creates `v0.1.0` on the public mirror via a custom SSH step (`s0/git-publish-subdir-action`'s `TAG_PREFIX` only *filters*, doesn't strip). Goreleaser triggers off `v*` on the public side.
- **Dry-run locally**: `goreleaser release --snapshot --clean --skip=publish` (needs `HOMEBREW_TAP_TOKEN=fake` env var set or the snapshot template fails when a matching private tag exists). `goreleaser check` validates YAML without building.
- **Use `homebrew_casks`, not `brews`** — `brews` is being phased out in goreleaser 2.x. Also avoid `homebrew_casks.binary` (also deprecated). The new cask format auto-supports Linux via `on_linux`.
- **Always set `repository.token: "{{ .Env.HOMEBREW_TAP_TOKEN }}"`** on `homebrew_casks` and `scoops` — the default `GITHUB_TOKEN` is scoped to `browzer-cli` only and returns 403 when writing to `homebrew-tap` / `scoop-bucket`.
- **errcheck idiom** (golangci-lint v2): deferred Close must be `defer func() { _ = f.Close() }()`, not `defer f.Close()`. Same for `_, _ = io.Copy(io.Discard, resp.Body)` on drain-before-retry paths. The action is pinned at `golangci/golangci-lint-action@v8` + `version: v2.5.0` because the default v1.64.8 is built with Go 1.24 and rejects `go 1.25.0` in go.mod.
- **`go mod tidy` can bump go.mod**: transitive deps like `golang.org/x/term@v0.41.0` require Go 1.25; don't manually downgrade the directive — update the CI `go-version` instead (currently `1.25`).
