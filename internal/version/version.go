// Package version exposes the CLI binary version as a single ldflag-injectable
// variable. Daemon RPC handlers (and any other surface that needs to advertise
// the build version) read this rather than threading the value through the
// commands package.
//
// Production builds inject the value via:
//
//	go build -ldflags "-X 'github.com/browzeremb/browzer-cli/internal/version.Version=1.11.0'" ./cmd/browzer
//
// Latest released tag: cli-v1.11.0 (skills+cli autoresearch sweep —
// 3 new named workflow queries closing every raw `jq … "$WORKFLOW"` pattern
// in skill bodies: `tasks-manifest`, `steps-by-name`, `steps-by-owner`.
// Query catalog grew from 10 → 13. Eight SKILL.md / reference files
// migrated to consume the new queries (or existing `next-step-id` /
// `get-config` / `get-step` surfaces). `scripts/audit/skill-no-raw-jq-workflow.mjs`
// is the metric (lower = better; baseline 33 → final 0). Tag with
// `git tag cli-v1.11.0 && git push origin cli-v1.11.0` once the
// release commit is pushed.
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
