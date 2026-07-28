// Package ui is a reusable Bubble Tea / Lip Gloss design-system component
// library. Every component is a pure function that returns a styled string and
// is width-aware where relevant. The package carries zero dependencies beyond
// the charmbracelet stack already present in go.mod.
package ui

import "github.com/charmbracelet/lipgloss"

// Palette. Central color constants shared by every component.
const (
	ColorBg      = lipgloss.Color("#0B0E14") // app background
	ColorSurface = lipgloss.Color("#151A23") // panel / card surface
	ColorBorder  = lipgloss.Color("#232A36") // subtle borders
	ColorPrimary = lipgloss.Color("#5EEAD4") // teal
	ColorAccent  = lipgloss.Color("#A78BFA") // violet
	ColorWarn    = lipgloss.Color("#FBBF24") // amber
	ColorDanger  = lipgloss.Color("#FB7185") // rose
	ColorText    = lipgloss.Color("#E6EDF3") // primary text
	ColorMuted   = lipgloss.Color("#7D8590") // secondary / muted text
)

// Reusable base styles. Callers may Copy()/Inherit() and override as needed.
var (
	// StyleBase applies the default text color on the app background.
	StyleBase = lipgloss.NewStyle().Foreground(ColorText)

	// StyleMuted renders secondary, de-emphasized text.
	StyleMuted = lipgloss.NewStyle().Foreground(ColorMuted)

	// StyleTitle renders a bold, primary-colored heading.
	StyleTitle = lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)

	// StyleSurface is a padded panel using the surface background.
	StyleSurface = lipgloss.NewStyle().
			Background(ColorSurface).
			Foreground(ColorText).
			Padding(0, 1)

	// StylePanel is a rounded-border container using the border color.
	StylePanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder).
			Foreground(ColorText).
			Padding(0, 1)
)
