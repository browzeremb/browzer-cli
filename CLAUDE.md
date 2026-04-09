# packages/cli — CLAUDE.md

Browzer CLI. **Written in Go, not Node.** Read the root `CLAUDE.md` first.

## Not part of the pnpm monorepo

- No `package.json`, no `node_modules`, does NOT participate in `pnpm turbo lint typecheck test`.
- Has its own `go.mod` (module `github.com/browzeremb/browzer-cli`) and goreleaser pipeline.
- Run its tests with `cd packages/cli && go test ./...`.
- Build locally with `cd packages/cli && go build -o "$HOME/.local/bin/browzer" ./cmd/browzer`.

## Shape

- `cmd/browzer/` — entrypoint (cobra root + subcommand wiring).
- `internal/commands/` — one file per subcommand.
- `internal/api/` — HTTP client against `apps/api` + `apps/auth`.
- `internal/auth/` — device flow client, token storage (`~/.browzer/credentials`).
- `internal/config/env.go` — `DefaultServer` honors `BROWZER_SERVER` env var; defaults to prod.
- `internal/walker/` — filesystem walker with gitignore + `isSensitive` filtering.
- `internal/upload/` — multipart upload helpers.
- `internal/urlvalidate/`, `internal/git/`, `internal/cache/`, `internal/output/`, `internal/ui/`, `internal/errors/` — support packages.

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

## Auth

- `browzer login` triggers the device flow against `apps/auth`. Credentials land in `~/.browzer/credentials` as JSON keyed by profile name (default: `default`).
- Smoke-test bearer: `jq -r .default.access_token ~/.browzer/credentials`.
