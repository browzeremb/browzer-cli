// Package version exposes the CLI binary version as a single ldflag-injectable
// variable. Daemon RPC handlers (and any other surface that needs to advertise
// the build version) read this rather than threading the value through the
// commands package.
//
// Production builds inject the value via:
//
//	go build -ldflags "-X 'github.com/browzeremb/browzer-cli/internal/version.Version=1.15.0'" ./cmd/browzer
//
// Latest released tag: cli-v1.15.0 (token-economy session followups +
// CLAUDE_SKILL_DIR/scripts convention). The cut bundles three CLI
// surfaces: (a) read-path JSON helpers (`marshalReadJSON` /
// `marshalNoHTMLEscape`) applied uniformly across every read verb so
// `<`, `>`, `&` in operator text round-trip literally to consumers
// piping through `jq`; (b) the `append-steps` plural mutator + the
// `describe-step` cobra alias for `describe-step-type`, plus
// `--payload -` honored as Unix-stdin convention via `readPayload`;
// (c) CUE validator hardening — `array-shape-mismatch` rewrites the
// cryptic "incompatible list lengths" error with the expected element
// shape, deterministic per-step fallback iteration in
// `lookupArrayElementFieldsForPath`, prefix-form normalization in
// `stepRootPath`, `stepNameAllowlist` derived from CUE `#StepName` at
// `loadSchema()`, and `workflow validate` routes through
// `schema.FormatViolations` for format symmetry with the apply path.
// Pre-mutation `stepId` validation in `mutatorAppendSteps` and a
// `Path` alias on `SearchResult` (server-tolerant via the new
// `PopulateSearchResultPaths` helper) round out the cut.
// Previous tag: cli-v1.13.0 (eval pipeline schema unification +
// on-behalf-of org-attribution trailer). Tag with
// `git tag cli-v1.15.0 && git push origin cli-v1.15.0` once the
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
