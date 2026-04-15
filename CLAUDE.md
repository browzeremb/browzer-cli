# packages/cli — CLAUDE.md

Browzer CLI. **Written in Go, not Node.** Read the root `CLAUDE.md` first.

## Not part of the pnpm monorepo

- No `package.json`, no `node_modules`, does NOT participate in `pnpm turbo lint typecheck test`.
- Has its own `go.mod` (module `github.com/browzeremb/browzer-cli`) and goreleaser pipeline.
- Run its tests with `cd packages/cli && go test ./...`.
- Build locally with `cd packages/cli && go build -o "$HOME/.local/bin/browzer" ./cmd/browzer`.

## Local verification (REQUIRED before pushing CLI changes)

The CLI's CI runs **four independent checks** that a plain `go test ./...` does NOT cover: `go vet`, `go test -race`, cross-compile for 5 targets (darwin/linux arm64+amd64, windows/amd64), and `golangci-lint v2.5.0`. Each of these has blocked past CI runs because the dev cycle never exercised them locally.

**Always run `make ci` before pushing** — it mirrors the public `browzeremb/browzer-cli` CI exactly:

```bash
cd packages/cli && make ci
```

On first run the script auto-installs `golangci-lint v2.5.0` into `$(go env GOPATH)/bin`. If `make ci` passes locally, `.github/workflows/ci.yml` on the public repo will pass too (same commands, same versions). Skipping this step is how you end up pushing a commit that only reveals its problem once it hits the remote runner. The script itself is at `packages/cli/scripts/ci-local.sh` and is the source of truth; the `Makefile` target is just an ergonomic entry point.

## Cross-platform discipline

`cmd/browzer` is built for 5 GOOS/GOARCH combos. Anything Unix-specific (`syscall.SysProcAttr.Setsid`, `os.Getuid()`-derived paths, `/tmp` hardcoding, `unix.*`) MUST be isolated behind `//go:build !windows` / `//go:build windows` file pairs. Pattern: helper file `foo_unix.go` (`//go:build !windows`) exports the function; `foo_windows.go` (`//go:build windows`) provides a no-op or Windows-equivalent stub. Example: `internal/commands/daemon_detach_unix.go` / `daemon_detach_windows.go`. `make ci` catches violations via the windows cross-compile step.

## Shape

- `cmd/browzer/` — entrypoint (cobra root + subcommand wiring).
- `internal/commands/` — one file per subcommand.
- `internal/api/` — HTTP client against `apps/api` + `apps/auth`.
- `internal/auth/` — device flow client, token storage (`~/.browzer/credentials`).
- `internal/config/env.go` — `DefaultServer` honors `BROWZER_SERVER` env var; defaults to prod.
- `internal/walker/` — filesystem walker with gitignore + `isSensitive` filtering.
- `internal/upload/` — multipart upload helpers.
- `internal/urlvalidate/`, `internal/git/` (includes `RealPath` — macOS case-insensitive path canonicalization), `internal/cache/`, `internal/output/`, `internal/ui/`, `internal/errors/` — support packages.

## Release flow

1. `git tag cli-v<semver> && git push origin cli-v<semver>` in this monorepo.
2. `.github/workflows/mirror-cli.yml` mirrors source to public `github.com/browzeremb/browzer-cli` and creates a stripped `v<semver>` tag there.
3. The public repo's `release.yml` runs goreleaser → GitHub Releases + `browzeremb/homebrew-tap` (cask) + `browzeremb/scoop-bucket` (manifest).
4. Watch the public-side run with `gh run watch <id> --repo browzeremb/browzer-cli`. Verify with `gh release view v<semver> --repo browzeremb/browzer-cli` — confirm `prerelease: false` for stable cuts.

### Secrets

- `MIRROR_SSH_PRIVATE_KEY` (this repo) — matches a write-enabled deploy key on `browzer-cli`.
- `HOMEBREW_TAP_TOKEN` (on the public `browzer-cli` repo) — fine-grained PAT with Contents:write on all three release repos (`browzer-cli`, `homebrew-tap`, `scoop-bucket`).

## Install script

`packages/cli/install.sh` is the source of `https://browzeremb.com/install.sh`. That URL is a 302 redirect configured in `apps/web/next.config.ts` → raw `install.sh` on the public mirror.

## Wire-format compatibility

The Go CLI replaced an earlier Node CLI. **Wire format (HTTP routes, JSON shapes, exit codes, file formats) is byte-compatible** with the Node version — changing any of these requires a coordinated server change.

### Recent wire-format additions

- `browzer explore` response now includes `exports`, `imports`, `importedBy`, `lines`, `score`, and `type` fields per entry. Older CLI versions ignore these (Go decoder drops unknown keys).
- `browzer deps <path>` — new command, calls `GET /api/workspaces/:id/deps`. Flags: `--reverse`, `--limit`, `--json`, `--save`, `--schema`.
- `browzer ask` — new command (G6 `3fb76e0`), implemented in `internal/commands/ask.go`. Sends `POST /api/ask` with a 3-tier `workspaceId` fallback: (1) `--workspace` flag, (2) `.browzer/config.json` `defaultWorkspaceId`, (3) `GET /api/workspaces` → first result. Never sends an empty `workspaceId` — errors explicitly if no workspace can be resolved. This ensures `apps/api`'s answer cache is engaged on every `ask` call.

### macOS case-sensitivity

`git.RealPath(path)` in `internal/git/git.go` resolves paths to their canonical filesystem casing by walking each component via `os.ReadDir`. Use this before `filepath.Rel(gitRoot, abs)` to avoid mismatches between `os.Getwd` (may return `desktop`) and git (returns `Desktop`). `FindGitRoot` applies it automatically.

## Auth

- `browzer login` triggers the device flow against `apps/auth`. Credentials land in `~/.browzer/credentials` as JSON keyed by profile name (default: `default`).
- Smoke-test bearer: `jq -r .default.access_token ~/.browzer/credentials`.
- `PollForToken` accepts a `Clock` interface (`internal/auth/clock.go`). Production callers pass `auth.RealClock{}`; tests inject `FakeClock` (defined in `device_flow_test.go`) to advance virtual time via `Advance(d)` — zero real sleeps, suite runs in <5s.
