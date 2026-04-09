package ui

import (
	"fmt"
	"io"
	"os"
)

// Status helpers render the ✓/✗/⚠/ℹ prefixes that sprinkle the CLI's
// long-running commands (init, sync, login). They ALL respect
// colorEnabled() so piped output stays parseable: in that case they
// fall back to plain ASCII markers. Every helper writes to stdout by
// default; use the *To variants when you need stderr routing.

// Success prints a green ✓ followed by msg, terminated by a newline.
func Success(msg string) {
	SuccessTo(os.Stdout, msg)
}

// SuccessTo is Success with an explicit writer (used by the tests and
// by sites that need stderr routing).
func SuccessTo(w io.Writer, msg string) {
	if !colorEnabled() {
		_, _ = fmt.Fprintf(w, "✓ %s\n", msg)
		return
	}
	_, _ = fmt.Fprintf(w, "%s %s\n",
		styleSuccess.Render("✓"),
		styleBody.Render(msg),
	)
}

// Failure prints a red ✗ followed by msg.
func Failure(msg string) {
	FailureTo(os.Stderr, msg)
}

// FailureTo writes a Failure line to an explicit writer.
func FailureTo(w io.Writer, msg string) {
	if !colorEnabled() {
		fmt.Fprintf(w, "✗ %s\n", msg)
		return
	}
	fmt.Fprintf(w, "%s %s\n",
		styleFailure.Render("✗"),
		styleBody.Render(msg),
	)
}

// Warn prints a yellow ⚠ followed by msg (stderr).
func Warn(msg string) {
	if !colorEnabled() {
		fmt.Fprintf(os.Stderr, "⚠ %s\n", msg)
		return
	}
	fmt.Fprintf(os.Stderr, "%s %s\n",
		styleWarn.Render("⚠"),
		styleBody.Render(msg),
	)
}

// Info prints a cyan ℹ followed by msg (stderr). Matches the
// existing cold-start hint marker so downstream consumers that grep
// for "ℹ" keep working.
func Info(msg string) {
	if !colorEnabled() {
		fmt.Fprintf(os.Stderr, "ℹ %s\n", msg)
		return
	}
	fmt.Fprintf(os.Stderr, "%s %s\n",
		styleInfo.Render("ℹ"),
		styleBody.Render(msg),
	)
}

// Arrow prints a colored → prefix. Used for transient step labels like
// "→ Workspace: ws-123".
func Arrow(msg string) {
	if !colorEnabled() {
		fmt.Printf("→ %s\n", msg)
		return
	}
	fmt.Printf("%s %s\n",
		styleSuccess.Render("→"),
		styleBody.Render(msg),
	)
}
