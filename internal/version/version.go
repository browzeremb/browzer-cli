// Package version exposes the CLI binary version as a single ldflag-injectable
// variable. Daemon RPC handlers (and any other surface that needs to advertise
// the build version) read this rather than threading the value through the
// commands package.
//
// Production builds inject the value via:
//
//	go build -ldflags "-X 'github.com/browzeremb/browzer-cli/internal/version.Version=1.12.0'" ./cmd/browzer
//
// Latest released tag: cli-v1.12.0 (RETRO §9 follow-up sweep —
// surfaces three new mutator-helper APIs in `internal/commands/workflow_mutator_helpers.go`:
// `enrichSetCurrentStepError` wraps `set-current-step` "step not found" errors
// with a "step not appended yet" hint + the last 5 known stepIds; `isParseError`
// + `emitParseErrorAudit` skip the daemon→standalone fallback on deterministic
// JSON parse failures (audit reason `parse_error_terminal`); `quietByDefaultUnderLLM`
// centralises the `--llm` / `BROWZER_LLM=1` predicate, first consumer being
// `acquireMutatorLock` which now silences the `--no-lock bypass active` warning
// under LLM mode. `scripts/audit/skill-cli-sync-drift.mjs` is the metric
// (lower = better; baseline 30 → final 0 across 16 iterations). Tag with
// `git tag cli-v1.12.0 && git push origin cli-v1.12.0` once the release
// commit is pushed.
//
// Empty default is acceptable in tests and dev (`go run`/`go test`); callers
// that need a non-empty fallback (e.g. user-facing `--version` output) should
// substitute "dev" themselves. The daemon's `Daemon.Version` JSON-RPC method
// returns this string verbatim so operators can correlate a running daemon
// with a known release.
package version

// Version is the CLI binary version. Default is "" so tests don't need to
// pin a specific value; production binaries override via the linker.
//
// Bump procedure:
//   1. Update the ldflag example above to the new version.
//   2. Update the "Latest released tag" line to match.
//   3. After commits are pushed: git tag cli-vX.Y.Z && git push origin cli-vX.Y.Z
//   4. mirror-cli.yml + goreleaser publish to homebrew + scoop automatically.
var Version = ""
