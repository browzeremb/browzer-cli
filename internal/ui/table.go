package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

// Table renders a simple bordered table with a brand-colored header
// row. Falls back to a tab-separated plain-text grid when color is
// disabled, so `browzer workspace list | awk` keeps working.
//
// headers and rows must be the same width; callers are expected to
// pre-stringify numeric columns.
func Table(headers []string, rows [][]string) string {
	if !colorEnabled() {
		return plainTable(headers, rows)
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(colorBorder)).
		Headers(headers...).
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			switch {
			case row == table.HeaderRow:
				return styleHead.Padding(0, 1)
			case col == 0:
				// First column gets body emphasis (id/name).
				return styleBody.Padding(0, 1)
			default:
				return styleDim.Padding(0, 1)
			}
		})
	return t.Render() + "\n"
}

// plainTable is the colorless fallback. Emits fixed-width columns
// (two-space gutter) which is what awk/column -t downstream callers
// already expect from the pre-color CLI.
func plainTable(headers []string, rows [][]string) string {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if i < len(widths) && len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
	}
	var sb strings.Builder
	writeRow := func(cells []string) {
		for i, c := range cells {
			sb.WriteString(c)
			if i < len(cells)-1 {
				pad := widths[i] - len(c) + 2
				for k := 0; k < pad; k++ {
					sb.WriteByte(' ')
				}
			}
		}
		sb.WriteByte('\n')
	}
	writeRow(headers)
	for _, r := range rows {
		writeRow(r)
	}
	return sb.String()
}
