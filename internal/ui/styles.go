package ui

import "github.com/charmbracelet/lipgloss"

// Shared style definitions so every helper in this package pulls from
// the same palette. Centralising here also makes it trivial to swap
// the brand palette in one place if the design system evolves.

var (
	colorSuccess = lipgloss.Color("#22c55e") // green-500 (brand primary)
	colorFailure = lipgloss.Color("#ef4444") // red-500
	colorWarn    = lipgloss.Color("#f59e0b") // amber-500
	colorInfo    = lipgloss.Color("#38bdf8") // sky-400
	colorBody    = lipgloss.Color("#e2e8f0") // slate-200
	colorDim     = lipgloss.Color("#94a3b8") // slate-400
	colorBorder  = lipgloss.Color("#334155") // slate-700

	styleSuccess = lipgloss.NewStyle().Foreground(colorSuccess).Bold(true)
	styleFailure = lipgloss.NewStyle().Foreground(colorFailure).Bold(true)
	styleWarn    = lipgloss.NewStyle().Foreground(colorWarn).Bold(true)
	styleInfo    = lipgloss.NewStyle().Foreground(colorInfo).Bold(true)
	styleBody    = lipgloss.NewStyle().Foreground(colorBody)
	styleDim     = lipgloss.NewStyle().Foreground(colorDim)
	styleHead    = lipgloss.NewStyle().Foreground(colorSuccess).Bold(true)
)
