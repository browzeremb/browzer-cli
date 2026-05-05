// Package version exposes the CLI binary version as a single ldflag-injectable
// variable. Daemon RPC handlers (and any other surface that needs to advertise
// the build version) read this rather than threading the value through the
// commands package.
//
// Production builds inject the value via:
//
//	go build -ldflags "-X 'github.com/browzeremb/browzer-cli/internal/version.Version=1.13.0'" ./cmd/browzer
//
// Latest released tag: cli-v1.13.0 (eval pipeline schema unification +
// on-behalf-of org-attribution trailer). The CLI itself ships unchanged
// in this cut — the bump rides alongside `skills-v4.12.0` because the
// monorepo bumps in lockstep — but two artefacts live under packages/cli/
// that the cut does carry: six CUE fixtures (3 valid + 3 invalid) added by
// `f1734147` for RETRO §2 drift classes + carve-outs, and the renamed
// canonical SKILL.md trailer recipe (`on-behalf-of: @browzeremb <...>` per
// GitHub's organization-commit doc, replacing the previous
// `Co-authored-by: browzeremb <...>` form which is reserved for human
// collaborators). `commit-coauthor.mjs` hook recognises both forms during
// the migration window. The skills-side eval pipeline (skills-v4.12.0)
// auto-discovers every `evals.json` and normalises three schema shapes
// (canonical {name,check}, typed {text,type,pattern|value}, prose
// expectations[]) into a single runner; pilot pass-rate on `commit/evals/`
// went from 0% (iter1) to 62% case-level / 81% assertion-level (iter7).
// Previous tag: cli-v1.12.0 (RETRO §9 follow-up sweep — three mutator-helper
// APIs in `internal/commands/workflow_mutator_helpers.go` —
// `enrichSetCurrentStepError`, `isParseError`+`emitParseErrorAudit`,
// `quietByDefaultUnderLLM`). Tag with `git tag cli-v1.13.0 && git push
// origin cli-v1.13.0` once the release commit is pushed.
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
