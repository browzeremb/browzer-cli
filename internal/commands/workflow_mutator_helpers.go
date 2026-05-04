package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/browzeremb/browzer-cli/internal/config"
	"github.com/browzeremb/browzer-cli/internal/daemon"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/tracker"
	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

// errLockTimeoutExitCode is a sentinel returned from RunE to signal that the
// cobra root should translate the error into exit code 16 (lock contention).
// The main.go handler detects CliError.ExitCode == 16.
var errLockTimeoutExitCode = &cliErrors.CliError{
	Message:  "workflow lock timeout",
	ExitCode: 16,
}

// acquireMutatorLock acquires the advisory lock for a workflow mutation command.
// If noLock is true, it emits a warning and returns nil (no lock held, lockHeld=0).
// On ErrLockTimeout it prints a message and returns errLockTimeoutExitCode.
func acquireMutatorLock(cmd *cobra.Command, wfPath string, noLock bool, timeout time.Duration) (*wf.Lock, time.Duration, error) {
	if noLock {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: --no-lock bypass active\n")
		return nil, 0, nil
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	lockStart := time.Now()
	lock, err := wf.NewLock(wfPath, timeout, cmd.ErrOrStderr())
	if err != nil {
		return nil, 0, err
	}
	if acquireErr := lock.Acquire(); acquireErr != nil {
		if errors.Is(acquireErr, wf.ErrLockTimeout) {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"lock timeout: another browzer workflow command is mutating %s\n", wfPath)
			return nil, 0, wf.ErrLockTimeout
		}
		return nil, 0, acquireErr
	}
	return lock, time.Since(lockStart), nil
}

// (saveWorkflow / findStepInRaw / recomputeCounters were the per-verb
// helpers used by the historic in-process write path. They moved into
// `internal/workflow/apply.go` (Mutators + ApplyAndPersist) when the
// dispatch helper landed in 2026-04-29; the daemon goroutine and the
// standalone fallback now share that single source of truth.)

// readPayload reads JSON payload from --payload flag file or stdin (when flag is "").
func readPayload(cmd *cobra.Command, payloadFlag string) ([]byte, error) {
	if payloadFlag != "" {
		return os.ReadFile(payloadFlag)
	}
	return io.ReadAll(cmd.InOrStdin())
}

// writeMode is the resolved per-call mode after considering CLI flags + config.
type writeMode string

const (
	// writeModeStandalone runs the mutation in-process via wf.ApplyAndPersist.
	// No daemon contact. Selected by `--sync` or when the daemon is unreachable
	// AND fallback is allowed.
	writeModeStandalone writeMode = "standalone"
	// writeModeDaemonAsync sends the mutation to the daemon, returns
	// immediately. Failures inside the drainer are silently lost.
	writeModeDaemonAsync writeMode = "daemon-async"
	// writeModeDaemonSync sends the mutation to the daemon AND blocks until
	// durable (fsync of file + parent dir).
	writeModeDaemonSync writeMode = "daemon-sync"
)

// resolveWriteMode picks the effective writeMode given the cobra flag set
// and (when none of --async/--sync/--await is present) the persisted config
// key `workflow.default_mode`. Default is "async".
//
// Mutual exclusion errors:
//
//	--sync + --async   → "--sync and --async are mutually exclusive"
//	--sync + --await   → "--sync and --await are mutually exclusive"
//	--async + --await  → --await wins (async is the LOOSEST guarantee, await
//	                      strictly upgrades; we don't error here because some
//	                      skill bodies sprinkle --async on every call AND
//	                      conditionally append --await on commit-blocking calls).
//
// Forward-compat: an unknown config value falls through to the default.
func resolveWriteMode(cmd *cobra.Command) (writeMode, error) {
	async, _ := cmd.Flags().GetBool("async")
	sync, _ := cmd.Flags().GetBool("sync")
	await, _ := cmd.Flags().GetBool("await")
	if !async {
		async, _ = cmd.InheritedFlags().GetBool("async")
	}
	if !sync {
		sync, _ = cmd.InheritedFlags().GetBool("sync")
	}
	if !await {
		await, _ = cmd.InheritedFlags().GetBool("await")
	}

	if sync && async {
		return "", errors.New("--sync and --async are mutually exclusive")
	}
	if sync && await {
		return "", errors.New("--sync and --await are mutually exclusive")
	}

	if sync {
		return writeModeStandalone, nil
	}
	if await {
		return writeModeDaemonSync, nil
	}
	if async {
		return writeModeDaemonAsync, nil
	}

	// Env-var override: BROWZER_WORKFLOW_MODE=sync|await|async.
	// Used by tests to force standalone (avoids the stale-daemon-binary
	// flakiness where dispatchToDaemonOrFallback routes to a daemon running
	// older code than the test's own ApplyAndPersist) and by CI environments
	// that don't want to spin up the daemon. Higher-priority than
	// config/default; lower-priority than explicit --sync/--async/--await.
	switch os.Getenv("BROWZER_WORKFLOW_MODE") {
	case "sync":
		return writeModeStandalone, nil
	case "await":
		return writeModeDaemonSync, nil
	case "async":
		return writeModeDaemonAsync, nil
	}

	switch readConfigString(config.ConfigKeyWorkflowDefaultMode) {
	case "sync":
		return writeModeStandalone, nil
	case "await":
		return writeModeDaemonSync, nil
	case "async":
		return writeModeDaemonAsync, nil
	}
	// No flag, no config — use the codified default.
	switch config.DefaultWorkflowMode {
	case "sync":
		return writeModeStandalone, nil
	case "await":
		return writeModeDaemonSync, nil
	default:
		return writeModeDaemonAsync, nil
	}
}

// daemonFallbackWarnOnce suppresses repeat stderr warnings when the daemon
// path is unavailable across multiple calls in a single CLI invocation.
// Reset on process boundaries (which is fine — agents re-fork per command).
var daemonFallbackWarnOnce sync.Once

// daemonVersionMismatchWarnOnce suppresses repeat protocol-mismatch warnings
// across multiple WorkflowMutate calls in the same process — the operator
// only needs to see "your daemon is stale, restart it" once per CLI run.
var daemonVersionMismatchWarnOnce sync.Once

// daemonVersionPreflight runs the one-shot `Daemon.Version` handshake. The
// `daemon.Client` already caches the response across calls (per Client
// lifetime), so subsequent invocations are zero-RPC — but we still wrap the
// call so the dispatch helper can read a single boolean ("speak v2?") plus
// the daemon-reported version for diagnostic logging.
//
// Returns:
//   - resp: the daemon's full Version response on success.
//   - err: any RPC error (dial / decode / `method_not_found` from a v1
//     daemon predating the handshake). Caller falls back to standalone on
//     ANY error.
//
// F-SE-6 (2026-05-04): the wrapper looks like a zero-logic pass-through
// today and a future contributor may be tempted to inline it. KEEP THE
// WRAPPER. It is the seam that lets us add diagnostic logging,
// telemetry, or a stricter context-deadline policy in one place without
// editing every call-site of dispatch. The 2026-05-04 review explicitly
// considered inlining and chose not to.
func daemonVersionPreflight(ctx context.Context, cli *daemon.Client) (daemon.DaemonVersionResponse, error) {
	return cli.DaemonVersion(ctx)
}

// flagBoolEither returns true if the named boolean flag is set to true on
// either the command's local FlagSet or its InheritedFlags.
//
// We check both Flags() and InheritedFlags() because the same flag may be
// locally re-declared on a sub-command without InheritedFlags() reflecting
// the override; defending against future flag-shadowing.
func flagBoolEither(cmd *cobra.Command, name string) bool {
	if v, err := cmd.Flags().GetBool(name); err == nil && v {
		return true
	}
	if v, err := cmd.InheritedFlags().GetBool(name); err == nil && v {
		return true
	}
	return false
}

// quietSource identifies which input silenced the audit line. Used by
// auditQuietSource() so callers can branch on the gate (e.g. SA-8: route
// the line through the SQLite tracker only when the LLM gate is active —
// preserving observability for high-frequency LLM-driven traffic).
type quietSource string

const (
	quietSourceNone     quietSource = ""
	quietSourceQuietFlag quietSource = "quiet-flag"
	quietSourceQuietEnv  quietSource = "quiet-env"
	quietSourceLLMEnv    quietSource = "llm-env"
	quietSourceLLMFlag   quietSource = "llm-flag"
)

// auditQuietRequested reports whether the per-mutation audit telemetry
// line should be suppressed for this invocation. See auditQuietSource()
// for which input gated the suppression.
func auditQuietRequested(cmd *cobra.Command) bool {
	_, src := auditQuietSource(cmd)
	return src != quietSourceNone
}

// auditQuietSource reports (quiet, source) where source identifies which
// input gated the suppression. Sources checked (any truthy → quiet):
//
//   - Four logical sources: --quiet, --llm, BROWZER_WORKFLOW_QUIET,
//     BROWZER_LLM; each flag is checked at both local and inherited scopes.
//
// Errors and fallback warnings still print on stderr regardless of this
// flag — exit code remains the authoritative success signal.
// There is no --no-quiet escape hatch; to re-enable audit, unset the env
// var or omit --quiet.
//
// Precedence: any source may set quiet to true; there is no way to opt out
// from an env var via the flag (env-set quiet is sticky for the process
// lifetime). Operators must `unset BROWZER_WORKFLOW_QUIET` if they want
// flag-driven control.
//
// Source precedence (when multiple are truthy): quiet-flag > quiet-env >
// llm-env > llm-flag. The order is significant only for telemetry routing:
// LLM-gated suppressions route to the SQLite tracker (SA-8) so observability
// survives the silenced stderr path.
func auditQuietSource(cmd *cobra.Command) (bool, quietSource) {
	if flagBoolEither(cmd, "quiet") {
		return true, quietSourceQuietFlag
	}
	if envBoolish(os.Getenv("BROWZER_WORKFLOW_QUIET")) {
		return true, quietSourceQuietEnv
	}
	if envBoolish(os.Getenv("BROWZER_LLM")) {
		return true, quietSourceLLMEnv
	}
	if flagBoolEither(cmd, "llm") {
		return true, quietSourceLLMFlag
	}
	return false, quietSourceNone
}

// isLLMGate reports whether the quiet source is one of the LLM-mode gates
// (--llm or BROWZER_LLM) — i.e. the operator asked to silence audit for
// LLM-context cleanliness, NOT for general quiet output. SA-8 routes audit
// data to the SQLite tracker only when this is true; explicit --quiet /
// BROWZER_WORKFLOW_QUIET genuinely want the line gone.
func isLLMGate(s quietSource) bool {
	return s == quietSourceLLMEnv || s == quietSourceLLMFlag
}

// envBoolish parses common truthy strings: 1, true, yes, on (case-insensitive).
// Empty / unset / 0 / false / no / off → false.
func envBoolish(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// dispatchToDaemonOrFallback runs verb on the daemon when feasible and
// falls back to the standalone path otherwise. Audit emission happens at
// the end of the chosen path; the verb's RunE no longer prints its own
// audit line.
//
// Dispatch decisions:
//
//  1. mode == standalone → run standalone path, emit audit with
//     mode=standalone-sync. (--sync explicitly chose this.)
//  2. mode == daemon-* → dial daemon. On dial failure / missing
//     workflow.v1 capability / queue_full: fall back to standalone with
//     mode=fallback-sync and a stderr-once warning.
//  3. daemon path succeeded → emit audit with the daemon's reported mode
//     (`daemon-async` | `daemon-sync`).
//
// The caller passes `verb`, `args.Args`, `args.Payload`, `args.JQExpr`
// already-populated. Caller also owns deciding whether `noLock` was
// requested — the daemon path REJECTS noLock=true so we surface that
// decision here: when `noLock=true` is set AND mode != standalone, we
// silently downgrade to standalone (the user explicitly asked for the
// lock bypass and the daemon won't honor it).
func dispatchToDaemonOrFallback(cmd *cobra.Command, wfPath, verb string, args wf.MutatorArgs, mode writeMode, noLock bool, lockTimeout time.Duration) error {
	stderr := cmd.ErrOrStderr()
	startedAt := time.Now()

	// --no-schema-check propagates from the workflow command group into
	// MutatorArgs.NoSchemaCheck. Daemon path does NOT yet plumb this
	// (TASK_06); when the flag is set AND mode != standalone, we silently
	// downgrade to standalone so the bypass actually takes effect AND the
	// audit line lands.
	if flagBoolEither(cmd, "no-schema-check") {
		args.NoSchemaCheck = true
		if mode != writeModeStandalone {
			mode = writeModeStandalone
		}
	}

	if noLock && mode != writeModeStandalone {
		// User explicitly asked to bypass the lock — the daemon path can't
		// honor it (would defeat the per-path FIFO). Silently downgrade so
		// the call still does what they asked.
		mode = writeModeStandalone
	}

	if mode == writeModeStandalone {
		return runStandaloneAndAudit(cmd, wfPath, verb, args, noLock, lockTimeout, wf.AuditModeStandaloneSync, "", startedAt)
	}

	cli := daemon.NewClient(config.SocketPath(os.Getuid()))
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()

	// F-SE-4 (2026-05-04): the HasCapability + DaemonVersion sequence
	// looks like a double round-trip but it is intentional and CHEAP:
	//   1. HasCapability is the legacy gate (caches Health for 60s); it
	//      tells us the daemon at all advertises `workflow.v1`. A daemon
	//      that fails Health entirely is detected here and we save the
	//      DaemonVersion RPC.
	//   2. DaemonVersion is the WF-SYNC-1 strict gate (per-Client cache,
	//      one RPC per CLI invocation in the success path).
	// Collapsing them into one call would conflate "daemon reachable" with
	// "daemon supports protocol v2" and obscure the fallback reason for
	// audit logs. Keep both checks.
	if !cli.HasCapability(ctx, "workflow.v1") {
		daemonFallbackWarnOnce.Do(func() {
			_, _ = fmt.Fprintln(stderr, "warn: daemon path unavailable (no workflow.v1) — falling back to standalone for this run")
		})
		return runStandaloneAndAudit(cmd, wfPath, verb, args, noLock, lockTimeout, wf.AuditModeFallbackSync, "daemon_unreachable", startedAt)
	}

	// Protocol-version handshake (WF-SYNC-1, 2026-05-04). Run before the
	// first WorkflowMutate of this Client's lifetime; subsequent calls hit
	// the cache and skip the RPC. On ANY error (RPC failure, v1 daemon that
	// returns method_not_found, decode error) we fall back to standalone —
	// the Daemon.Version method is the contract gate, not a soft hint.
	if vresp, vErr := daemonVersionPreflight(ctx, cli); vErr != nil {
		daemonFallbackWarnOnce.Do(func() {
			_, _ = fmt.Fprintf(stderr, "warn: daemon version preflight failed (%v) — falling back to standalone for this run\n", vErr)
		})
		return runStandaloneAndAudit(cmd, wfPath, verb, args, noLock, lockTimeout, wf.AuditModeFallbackSync, "daemon_version_unavailable", startedAt)
	} else if vresp.ProtocolVersion != daemon.CurrentProtocolVersion {
		daemonVersionMismatchWarnOnce.Do(func() {
			_, _ = fmt.Fprintln(stderr,
				"warn: daemon protocol mismatch (expected v"+strconv.Itoa(daemon.CurrentProtocolVersion)+", got v"+strconv.Itoa(vresp.ProtocolVersion)+") — falling back to standalone")
		})
		return runStandaloneAndAudit(cmd, wfPath, verb, args, noLock, lockTimeout, wf.AuditModeFallbackSync, "daemon_protocol_mismatch", startedAt)
	}

	awaitDurability := mode == writeModeDaemonSync
	res, err := cli.WorkflowMutate(ctx, daemon.WorkflowMutateParams{
		Verb:            verb,
		Path:            wfPath,
		Payload:         json.RawMessage(args.Payload),
		Args:            args.Args,
		JQExpr:          args.JQExpr,
		JQVars:          args.JQVars,
		ProtocolVersion: daemon.CurrentProtocolVersion,
		AwaitDurability: awaitDurability,
		LockTimeoutMs:   lockTimeout.Milliseconds(),
		WriteID:         newWriteID(),
	})
	if err != nil {
		// queue_full / unknown_verb / timeout / etc. Surface the reason in
		// the audit line and fall back to standalone (which re-applies
		// idempotently on retried verbs).
		daemonFallbackWarnOnce.Do(func() {
			_, _ = fmt.Fprintf(stderr, "warn: daemon WorkflowMutate failed (%v) — falling back to standalone for this run\n", err)
		})
		return runStandaloneAndAudit(cmd, wfPath, verb, args, noLock, lockTimeout, wf.AuditModeFallbackSync, daemonErrorReason(err), startedAt)
	}

	auditMode := wf.AuditModeDaemonAsync
	if awaitDurability {
		auditMode = wf.AuditModeDaemonSync
	}
	line := wf.AuditLine{
		Verb:            verb,
		Path:            wfPath,
		Mode:            auditMode,
		WriteID:         res.WriteID,
		StepID:          res.StepID,
		LockHeldMs:      res.LockHeldMs,
		ValidatedOk:     res.ValidatedOk,
		Durable:         res.Durable,
		QueueDepthAhead: res.QueueDepthAhead,
		ElapsedMs:       time.Since(startedAt).Milliseconds(),
	}
	emitAuditLine(cmd, stderr, line)
	return nil
}

// runStandaloneAndAudit is the unified standalone path used by
// `--sync` and by the daemon-failure fallback. It acquires the lock, runs
// `wf.ApplyAndPersist`, releases the lock, and emits one audit line.
func runStandaloneAndAudit(cmd *cobra.Command, wfPath, verb string, args wf.MutatorArgs, noLock bool, lockTimeout time.Duration, mode wf.AuditMode, reason string, startedAt time.Time) error {
	stderr := cmd.ErrOrStderr()

	lock, lockHeld, lockErr := acquireMutatorLock(cmd, wfPath, noLock, lockTimeout)
	if lockErr != nil {
		if lockErr == wf.ErrLockTimeout {
			return errLockTimeoutExitCode
		}
		return lockErr
	}
	if lock != nil {
		defer func() { _ = lock.Release() }()
	}

	// Standalone path always honors awaitDurability=true when the user
	// explicitly asked for `--sync` from a daemon-fsync-aware code path
	// (we receive mode=fallback-sync in that case AND the user wanted
	// durability). Simpler rule: standalone never fsyncs (matches the
	// historic CLI behaviour). Callers that need durability must use the
	// daemon path.
	res, err := wf.ApplyAndPersist(wfPath, verb, args, false /* awaitDurability */)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "validation or write error: %v\n", err)
		return err
	}

	// Compose audit reason: prefer the dispatch-decision reason
	// (fallback / queue_full); fall back to the mutator's own NoOp reason
	// so idempotent skips show up in the line.
	auditReason := reason
	if auditReason == "" && res.NoOp {
		auditReason = "noop:" + res.NoOpReason
	}
	if res.NoOp && res.NoOpReason == "already_completed" {
		// Backwards-compat: legacy CLI emitted a stderr warning when
		// complete-step was a no-op. Existing tests + skill bodies grep
		// stderr for "already"/"completed"/"no-op". Keep the line.
		_, _ = fmt.Fprintf(stderr,
			"warn: step %q is already COMPLETED (idempotent no-op)\n", res.StepID)
	}

	line := wf.AuditLine{
		Verb:        verb,
		Path:        wfPath,
		Mode:        mode,
		StepID:      res.StepID,
		LockHeldMs:  lockHeld.Milliseconds(),
		ValidatedOk: res.ValidatedOk,
		Durable:     res.Durable,
		ElapsedMs:   time.Since(startedAt).Milliseconds(),
		Reason:      auditReason,
	}
	emitAuditLine(cmd, stderr, line)
	return nil
}

// emitAuditLine writes the workflow-mutation audit line to the right sink:
//
//   - Not silenced (no --quiet, no --llm, no env override) → stderr verbatim
//     (legacy behaviour; observability via shell pipelines).
//   - Silenced via --llm / BROWZER_LLM (LLM-mode gate) → SQLite tracker via
//     tracker.Record(). Stderr stays clean (LLM tool-result context isn't
//     polluted) but the data lands in the events table for `browzer gain`
//     aggregation. SA-8 closure.
//   - Silenced via --quiet / BROWZER_WORKFLOW_QUIET (operator explicitly
//     wants the line gone) → drop. Operator chose silence.
//
// Tracker errors are best-effort and never propagate — observability is a
// "nice to have" on the silenced path; if SQLite is locked / disk full /
// schema-mismatched, the calling mutation already succeeded and we don't
// want to fail it on telemetry. A failed Record bumps a single-line warning
// once per process via trackerWarnOnce.
func emitAuditLine(cmd *cobra.Command, stderr io.Writer, line wf.AuditLine) {
	quiet, src := auditQuietSource(cmd)
	if !quiet {
		wf.WriteAudit(stderr, line)
		return
	}
	if isLLMGate(src) {
		recordAuditEventBestEffort(line, string(src))
	}
}

// trackerWarnOnce limits "tracker.Record failed" warnings to one per process
// boundary so high-frequency LLM-driven traffic doesn't spam stderr.
var trackerWarnOnce sync.Once

// recordAuditEventBestEffort persists one workflow-mutation audit line into
// the SQLite tracker. Uses a fresh Open/Close per call — the daemon owns the
// long-lived handle, but the CLI on the LLM-gate path is short-lived so a
// per-call open is fine (WAL mode + the tracker's internal mutex handle the
// concurrency).
//
// The Event shape is intentionally minimal: source="workflow-audit", command
// is the verb (`patch`, `set-status`, `query`, ...), exec_ms is the audit
// line's ElapsedMs, and the path-hash + writeId are stuffed into PathHash /
// SessionID respectively so the gain query can group by them when desired.
// Token-economy fields (input/output bytes, savings) are zero — workflow
// mutations are not the savings surface, but they ARE traffic that operators
// running with --llm want visibility into.
func recordAuditEventBestEffort(line wf.AuditLine, src string) {
	tr, err := tracker.Open(config.HistoryDBPath())
	if err != nil {
		trackerWarnOnce.Do(func() {
			_, _ = fmt.Fprintf(os.Stderr, "warn: workflow audit tracker open failed (%v) — telemetry on the LLM-gate path is dropped for this run\n", err)
		})
		return
	}
	defer func() { _ = tr.Close() }()

	pathHash := hashPathForTracker(line.Path)
	writeID := line.WriteID
	cmdStr := line.Verb
	if line.Reason != "" {
		cmdStr = line.Verb + ":" + line.Reason
	}
	stepID := line.StepID
	ev := tracker.Event{
		TS:          time.Now().UTC().Format(time.RFC3339),
		Source:      "workflow-audit:" + src,
		Command:     cmdStr,
		PathHash:    &pathHash,
		ExecMs:      int(line.ElapsedMs),
		WorkspaceID: &stepID,
		SessionID:   &writeID,
	}
	if err := tr.Record(ev); err != nil {
		trackerWarnOnce.Do(func() {
			_, _ = fmt.Fprintf(os.Stderr, "warn: workflow audit tracker record failed (%v) — telemetry on the LLM-gate path is dropped for this run\n", err)
		})
	}
}

// hashPathForTracker returns a stable short hash of an absolute workflow.json
// path. The tracker schema treats path_hash as opaque; we want grouping by
// path without persisting the full path (operator $HOME / org names leak
// otherwise). FNV-1a is fast and collision-stable for our small N (paths
// touched in one CLI process).
func hashPathForTracker(p string) string {
	if p == "" {
		return ""
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(p))
	return fmt.Sprintf("%016x", h.Sum64())
}

// daemonErrorReason converts a JSON-RPC error string into an audit-line
// reason field. The full surface is `queue_full | timeout |
// noLock_unsupported_in_daemon_path | unknown_verb |
// workflow_dispatcher_disabled | other`.
func daemonErrorReason(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "queue_full"):
		return "queue_full"
	case strings.Contains(msg, "timeout"):
		return "daemon_timeout"
	case strings.Contains(msg, "noLock_unsupported_in_daemon_path"):
		return "noLock_unsupported"
	case strings.Contains(msg, "unknown_verb"):
		return "unknown_verb"
	case strings.Contains(msg, "workflow_dispatcher_disabled"):
		return "dispatcher_disabled"
	case strings.Contains(msg, "method_not_found"):
		return "method_not_found"
	case strings.Contains(msg, "dial daemon"):
		return "daemon_unreachable"
	default:
		return "daemon_error"
	}
}

// newWriteID returns a short opaque correlation id for one mutation. We
// don't need ULID/UUID globally-unique guarantees — just a per-process
// monotonic counter combined with the start nanos so audit lines line up
// across daemon + CLI emissions.
func newWriteID() string {
	n := writeIDCounter.Add(1)
	return fmt.Sprintf("wf-%x-%x", time.Now().UnixNano(), n)
}

var writeIDCounter atomic.Int64
