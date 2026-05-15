package output

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// EnvLLMEnabled reports whether BROWZER_LLM requests LLM mode. Presence
// alone is NOT enough — we parse the value so callers can set
// `BROWZER_LLM=0` (or `false`/`off`/empty) to explicitly disable,
// unlike NO_COLOR where presence is the signal. The truthy set matches
// GNU-ish conventions: 1, true, yes, on (case-insensitive).
//
// This is the single authoritative parser for BROWZER_LLM truthy
// semantics. All call sites that need to detect LLM mode from the
// environment MUST use this function rather than inlining the switch.
func EnvLLMEnabled() bool {
	v, ok := os.LookupEnv("BROWZER_LLM")
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// RegisterQuietFlag adds a `--quiet` boolean flag to the given cobra
// command. The flag is accepted everywhere a top-level read verb may be
// invoked so a single cross-cutting `browzer <verb> --quiet` invocation
// never fails with "unknown flag: --quiet" — even when the verb has no
// audit / staleness / banner output to silence today.
//
// Rationale (RETRO §16 #1): parallel agent batches mix verbs (e.g.
// `status --quiet` + `explore --quiet` + `workflow get-step --quiet`).
// A single rejection cancels the whole batch, so the flag MUST be
// accepted by every verb. Where suppression has nothing to do, the
// flag is silently a no-op.
//
// Verbs that already define their own --quiet flag (search, explore,
// workflow group) MUST NOT call this helper — duplicate registration
// panics in cobra at startup. Callers should check Lookup("quiet")
// before delegating.
func RegisterQuietFlag(cmd *cobra.Command) {
	if cmd.Flags().Lookup("quiet") != nil {
		return
	}
	cmd.Flags().Bool("quiet", false, "suppress info-level stderr output (errors still print); accepted as a no-op when there is nothing to silence")
}

// QuietRequested reports whether --quiet was set on the given command.
// Returns false (silently) when the flag is not registered, so callers
// can use this helper unconditionally without crashing on commands
// where the flag wasn't wired.
func QuietRequested(cmd *cobra.Command) bool {
	if cmd == nil || cmd.Flags().Lookup("quiet") == nil {
		return false
	}
	v, _ := cmd.Flags().GetBool("quiet")
	return v
}

// Verbose holds the global verbosity count from the `-v` flag.
// 0 = quiet (default), 1 = decisions, 2 = subprocess details, 3 = raw I/O.
var Verbose int

// Debugf writes to stderr when Verbose >= 1.
func Debugf(format string, args ...any) {
	if Verbose >= 1 {
		fmt.Fprintf(os.Stderr, "[browzer] "+format+"\n", args...)
	}
}

// Tracef writes to stderr when Verbose >= 2.
func Tracef(format string, args ...any) {
	if Verbose >= 2 {
		fmt.Fprintf(os.Stderr, "[browzer:trace] "+format+"\n", args...)
	}
}

// Rawf writes to stderr when Verbose >= 3.
func Rawf(format string, args ...any) {
	if Verbose >= 3 {
		fmt.Fprintf(os.Stderr, "[browzer:raw] "+format+"\n", args...)
	}
}

// DumpRaw writes the body to the given writer prefixed with a header,
// when Verbose >= 3. Used by `read` and the daemon client to dump
// pre-filter / post-filter content.
func DumpRaw(w io.Writer, header string, body []byte) {
	if Verbose >= 3 {
		_, _ = fmt.Fprintf(w, "--- %s (%d bytes) ---\n", header, len(body))
		_, _ = w.Write(body)
		_, _ = fmt.Fprintln(w)
	}
}

// EmitStalenessWarningTo writes the staleness warning to the given
// writer when:
//
//   - quiet is false (operator did NOT pass --quiet), AND
//   - stale is true (the index is at least one commit behind HEAD).
//
// The writer MUST be stderr (or a stderr-equivalent) at all real call
// sites — RETRO §16 #5 contract: the warning must NEVER reach stdout,
// because `--save <path> --json` callers parse stdout as JSON and a
// stray "⚠ Index N commits behind…" line breaks them.
//
// Encapsulating the routing here keeps the four call sites (explore,
// search, deps, mentions) consistent and gives unit tests one well-
// known target instead of four. Returns true iff a warning was written
// — useful for tests and (eventually) telemetry.
func EmitStalenessWarningTo(w io.Writer, quiet, stale bool, commitsBehind int) bool {
	if quiet || !stale {
		return false
	}
	_, _ = fmt.Fprint(w, FormatStalenessWarning(commitsBehind))
	return true
}
