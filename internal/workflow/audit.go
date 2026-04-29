// Package workflow — audit.go
//
// AuditLine + WriteAudit are the single source of truth for the stderr line
// each workflow mutation emits. The format is a strict superset of the
// pre-2026-04-29 line:
//
//	verb=X path=Y mode=daemon-async|daemon-sync|fallback-sync|standalone-sync \
//	     writeId=Z stepId=… lockHeldMs=N validatedOk=B durable=B \
//	     queueDepthAhead=N elapsedMs=N reason=…
//
// Skill stderr parsers (Node) match `verb=` as the first field and read
// `validatedOk=`. Both stay first/required so existing parsers keep working.
// New fields are appended in stable order; missing values are emitted as
// empty (so a stale parser still sees the same field count when present).
package workflow

import (
	"fmt"
	"io"
	"strings"
)

// AuditMode is the dispatch mode the line was emitted under.
type AuditMode string

const (
	// AuditModeDaemonAsync — daemon accepted the job, did NOT block on completion.
	AuditModeDaemonAsync AuditMode = "daemon-async"
	// AuditModeDaemonSync — daemon accepted and the caller blocked on durability.
	AuditModeDaemonSync AuditMode = "daemon-sync"
	// AuditModeFallbackSync — daemon path was attempted but unreachable / declined,
	// and the CLI fell back to a same-process synchronous write.
	AuditModeFallbackSync AuditMode = "fallback-sync"
	// AuditModeStandaloneSync — the historic, no-daemon path. Used when the user
	// passes `--sync` explicitly OR the daemon path is disabled by config.
	AuditModeStandaloneSync AuditMode = "standalone-sync"
)

// AuditLine carries the values emitted by WriteAudit. Zero values for
// optional numeric / string fields are rendered as empty so stale parsers
// don't trip over missing keys (key always present, value sometimes empty).
type AuditLine struct {
	Verb            string
	Path            string
	Mode            AuditMode
	WriteID         string
	StepID          string
	LockHeldMs      int64
	ValidatedOk     bool
	Durable         bool
	QueueDepthAhead int64
	ElapsedMs       int64
	// Reason is set only when something deviated from the happy path
	// (e.g. fallback after queue_full, no-op idempotent skip). Empty for
	// successful primary-path writes.
	Reason string
}

// WriteAudit emits a single line ending with '\n' describing one mutation.
// Empty values for optional fields render as `key=` (key present, value
// empty) so downstream awk/grep recipes stay stable.
//
// Field order:
//
//	verb path mode writeId stepId lockHeldMs validatedOk durable
//	queueDepthAhead elapsedMs reason
//
// Reason is suppressed entirely when empty so the common case stays terse;
// every other field is always present.
func WriteAudit(w io.Writer, a AuditLine) {
	var b strings.Builder
	b.Grow(192)
	// verb FIRST — Node parsers anchor on this.
	fmt.Fprintf(&b, "verb=%s", a.Verb)
	if a.Path != "" {
		fmt.Fprintf(&b, " path=%s", auditQuote(a.Path))
	} else {
		b.WriteString(" path=")
	}
	if a.Mode != "" {
		fmt.Fprintf(&b, " mode=%s", a.Mode)
	} else {
		b.WriteString(" mode=")
	}
	if a.WriteID != "" {
		fmt.Fprintf(&b, " writeId=%s", a.WriteID)
	} else {
		b.WriteString(" writeId=")
	}
	if a.StepID != "" {
		fmt.Fprintf(&b, " stepId=%s", a.StepID)
	} else {
		b.WriteString(" stepId=")
	}
	fmt.Fprintf(&b, " lockHeldMs=%d", a.LockHeldMs)
	// validatedOk is REQUIRED by Node parsers — keep verbatim "true"|"false".
	fmt.Fprintf(&b, " validatedOk=%t", a.ValidatedOk)
	fmt.Fprintf(&b, " durable=%t", a.Durable)
	fmt.Fprintf(&b, " queueDepthAhead=%d", a.QueueDepthAhead)
	fmt.Fprintf(&b, " elapsedMs=%d", a.ElapsedMs)
	if a.Reason != "" {
		fmt.Fprintf(&b, " reason=%s", auditQuote(a.Reason))
	}
	b.WriteByte('\n')
	_, _ = w.Write([]byte(b.String()))
}

// auditQuote renders a value safe for the key=value audit line.
// If the value contains a space, '=', '"', or starts/ends with whitespace,
// it is wrapped in double quotes with embedded quotes/backslashes escaped.
// Otherwise it is emitted verbatim.
func auditQuote(s string) string {
	needs := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '"' || c == '=' || c == '\\' {
			needs = true
			break
		}
	}
	if !needs {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' || c == '\\' {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	b.WriteByte('"')
	return b.String()
}
