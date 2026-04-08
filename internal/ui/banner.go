// Package ui owns terminal presentation for the Browzer CLI: the
// brand banner, color palette, and any shared lipgloss styles. Kept
// separate from internal/output (which handles JSON/--save routing)
// so that stylistic changes don't touch machine-readable codepaths.
package ui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// Wordmark ported from apps/web/src/components/brand-console-banner.tsx
// — keep the two in sync when the brand changes. Six lines of block
// figlet; the leading blank line is intentional so callers don't need
// to add their own spacing.
const wordmark = `
  ██████╗ ██████╗  ██████╗ ██╗    ██╗███████╗███████╗██████╗
  ██╔══██╗██╔══██╗██╔═══██╗██║    ██║╚══███╔╝██╔════╝██╔══██╗
  ██████╔╝██████╔╝██║   ██║██║ █╗ ██║  ███╔╝ █████╗  ██████╔╝
  ██╔══██╗██╔══██╗██║   ██║██║███╗██║ ███╔╝  ██╔══╝  ██╔══██╗
  ██████╔╝██║  ██║╚██████╔╝╚███╔███╔╝███████╗███████╗██║  ██║
  ╚═════╝ ╚═╝  ╚═╝ ╚═════╝  ╚══╝╚══╝ ╚══════╝╚══════╝╚═╝  ╚═╝
`

// colorEnabled reports whether we should emit ANSI sequences. False
// when stdout is redirected (pipe, file), when NO_COLOR is set per the
// no-color.org convention, or when TERM=dumb. This is what keeps
// `browzer ... --json | jq` and CI logs clean.
func colorEnabled() bool {
	if _, noColor := os.LookupEnv("NO_COLOR"); noColor {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// Banner returns the full brand banner ready to print: wordmark +
// tagline + hiring hint. Plain ASCII when colorEnabled() is false so
// piped output stays parseable.
func Banner(version string) string {
	tagline := "// precision rag · hybrid vector + graph retrieval"
	versionLine := "browzer cli " + version

	if !colorEnabled() {
		var b strings.Builder
		b.WriteString(wordmark)
		b.WriteString("  ")
		b.WriteString(versionLine)
		b.WriteString("\n  ")
		b.WriteString(tagline)
		b.WriteString("\n")
		return b.String()
	}

	art := lipgloss.NewStyle().Foreground(colorSuccess).Render(wordmark)
	ver := styleDim.Render("  " + versionLine)
	tag := lipgloss.NewStyle().Foreground(colorDim).Italic(true).Render("  " + tagline)
	return art + "\n" + ver + "\n" + tag + "\n"
}

// Heading returns a bold, brand-colored heading for section titles
// (e.g. "commands:" in the help template). Falls back to the raw
// string when color is disabled. Exposed as a cobra template
// function so SetUsageTemplate can colorize section labels.
func Heading(s string) string {
	if !colorEnabled() {
		return s
	}
	return styleHead.Render(s)
}
