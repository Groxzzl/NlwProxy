package pages

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// Palette — the NlwProxy control-plane theme.
//
//	bg #0B0E14  surface #151A23  primary #5EEAD4  accent #A78BFA
//	warn #FBBF24  danger #FB7185  text #E6EDF3  muted #7D8590
var (
	colBG      = lipgloss.Color("#0B0E14")
	colSurface = lipgloss.Color("#151A23")
	colPrimary = lipgloss.Color("#5EEAD4")
	colAccent  = lipgloss.Color("#A78BFA")
	colWarn    = lipgloss.Color("#FBBF24")
	colDanger  = lipgloss.Color("#FB7185")
	colText    = lipgloss.Color("#E6EDF3")
	colMuted   = lipgloss.Color("#7D8590")

	accent  = lipgloss.NewStyle().Foreground(colPrimary).Bold(true)
	muted   = lipgloss.NewStyle().Foreground(colMuted)
	good    = lipgloss.NewStyle().Foreground(colPrimary)
	warn    = lipgloss.NewStyle().Foreground(colWarn)
	bad     = lipgloss.NewStyle().Foreground(colDanger)
	cursor  = lipgloss.NewStyle().Foreground(colPrimary).Bold(true)
	violet  = lipgloss.NewStyle().Foreground(colAccent)
	textCol = lipgloss.NewStyle().Foreground(colText)
	header  = lipgloss.NewStyle().Foreground(colMuted).Bold(true)
)

func Title(name, subtitle string) string {
	if subtitle == "" {
		return accent.Render(name)
	}
	return accent.Render(name) + "  " + muted.Render(subtitle)
}

func StatusCode(code int) string {
	if code >= 200 && code < 400 {
		return good.Render(fmtInt(code))
	}
	return bad.Render(fmtInt(code))
}

func clip(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

// compactDuration renders a coarse human span such as 13h42m, 4m11s, or 9s.
// It is used for cooldown countdowns where sub-second precision is noise.
func compactDuration(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	d = d.Round(time.Second)
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	s := int((d % time.Minute) / time.Second)
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm%02ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

func fmtInt(n int) string {
	if n == 0 {
		return "—"
	}
	const digits = "0123456789"
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = digits[n%10]
		n /= 10
	}
	return string(b[i:])
}
