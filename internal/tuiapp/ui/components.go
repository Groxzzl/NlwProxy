package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// BadgeKind classifies a Badge's semantic state, driving its color and glyph.
type BadgeKind string

const (
	KindHealthy  BadgeKind = "healthy"
	KindSlow     BadgeKind = "slow"
	KindDead     BadgeKind = "dead"
	KindCooldown BadgeKind = "cooldown"
	KindOnline   BadgeKind = "online"
	KindOffline  BadgeKind = "offline"
)

// badgeColor maps a BadgeKind to its accent color, defaulting to muted.
func badgeColor(kind BadgeKind) lipgloss.Color {
	switch kind {
	case KindHealthy, KindOnline:
		return ColorPrimary
	case KindSlow, KindCooldown:
		return ColorWarn
	case KindDead, KindOffline:
		return ColorDanger
	default:
		return ColorMuted
	}
}

// Badge renders a pill like "● HEALTHY" colored by kind. The label is
// uppercased for a consistent, badge-like appearance.
func Badge(label string, kind BadgeKind) string {
	style := lipgloss.NewStyle().Foreground(badgeColor(kind)).Bold(true)
	text := strings.ToUpper(strings.TrimSpace(label))
	return style.Render("● " + text)
}

// KindGlyph returns the canonical status glyph for a BadgeKind:
// ● healthy/online, ◐ slow, ✗ dead/offline, ❄ cooldown.
func KindGlyph(kind BadgeKind) string {
	switch kind {
	case KindSlow:
		return "◐"
	case KindDead, KindOffline:
		return "✗"
	case KindCooldown:
		return "❄"
	default:
		return "●"
	}
}

// BadgeGlyph renders a pill like "◐ SLOW" using a caller-supplied glyph while
// still coloring by kind. Use it when the default ● glyph is not desired.
func BadgeGlyph(glyph, label string, kind BadgeKind) string {
	style := lipgloss.NewStyle().Foreground(badgeColor(kind)).Bold(true)
	text := strings.ToUpper(strings.TrimSpace(label))
	if glyph == "" {
		return style.Render(text)
	}
	return style.Render(glyph + " " + text)
}

// Chip renders a filter chip like [healthy]. When active it is filled with the
// kind color and dark text; when inactive it is a muted outline label so the
// active choice stands out at a glance.
func Chip(label string, active bool, kind BadgeKind) string {
	if active {
		return lipgloss.NewStyle().
			Foreground(ColorBg).Background(badgeColor(kind)).Bold(true).
			Padding(0, 1).Render(label)
	}
	return lipgloss.NewStyle().Foreground(ColorMuted).Padding(0, 1).Render(label)
}

// SegmentBar renders proportional colored blocks for labeled counts across
// width. Segments render in order; zero-count segments are skipped. It is used
// for health composition bars (healthy/slow/dead/cooldown).
type Segment struct {
	Count int
	Kind  BadgeKind
}

// SegmentBar draws a stacked proportional bar from the given segments.
func SegmentBar(segments []Segment, width int) string {
	if width < 1 {
		width = 1
	}
	total := 0
	for _, s := range segments {
		if s.Count > 0 {
			total += s.Count
		}
	}
	if total == 0 {
		return lipgloss.NewStyle().Foreground(ColorBorder).Render(strings.Repeat("░", width))
	}
	var b strings.Builder
	used := 0
	last := -1
	for i, s := range segments {
		if s.Count > 0 {
			last = i
		}
	}
	for i, s := range segments {
		if s.Count <= 0 {
			continue
		}
		n := s.Count * width / total
		if n < 1 {
			n = 1
		}
		if i == last {
			n = width - used
		}
		if used+n > width {
			n = width - used
		}
		if n <= 0 {
			continue
		}
		b.WriteString(lipgloss.NewStyle().Foreground(badgeColor(s.Kind)).Render(strings.Repeat("█", n)))
		used += n
	}
	if used < width {
		b.WriteString(lipgloss.NewStyle().Foreground(ColorBorder).Render(strings.Repeat("░", width-used)))
	}
	return b.String()
}

// ProgressBar renders a determinate bar like "▓▓▓░░ 124/206". width is the
// total cell budget for the whole component (bar + count); the bar auto-sizes
// to fill whatever remains after the count. done is clamped to [0, total].
func ProgressBar(done, total, width int) string {
	if done < 0 {
		done = 0
	}
	if total < 0 {
		total = 0
	}
	if done > total {
		done = total
	}

	count := fmt.Sprintf("%d/%d", done, total)
	// Reserve space for the count plus one separating space.
	barWidth := width - runewidth.StringWidth(count) - 1
	if barWidth < 1 {
		barWidth = 1
	}

	var frac float64
	if total > 0 {
		frac = float64(done) / float64(total)
	}
	filled := int(frac * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	empty := barWidth - filled

	fill := lipgloss.NewStyle().Foreground(ColorPrimary).Render(strings.Repeat("▓", filled))
	rest := lipgloss.NewStyle().Foreground(ColorBorder).Render(strings.Repeat("░", empty))
	label := StyleMuted.Render(count)
	return fill + rest + " " + label
}

// sparkLevels are the eight ascending block glyphs used by Sparkline.
var sparkLevels = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// Sparkline renders a compact unicode block sparkline from the given values.
// The series is normalized across its own min/max range. An empty series
// yields an empty string.
func Sparkline(values []float64) string {
	if len(values) == 0 {
		return ""
	}

	min, max := values[0], values[0]
	for _, v := range values {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	span := max - min

	var b strings.Builder
	for _, v := range values {
		var idx int
		if span > 0 {
			idx = int((v - min) / span * float64(len(sparkLevels)-1))
		}
		if idx < 0 {
			idx = 0
		}
		if idx >= len(sparkLevels) {
			idx = len(sparkLevels) - 1
		}
		b.WriteRune(sparkLevels[idx])
	}
	return lipgloss.NewStyle().Foreground(ColorAccent).Render(b.String())
}

// Card renders a rounded-border panel with a title header and a body. width is
// the outer width of the panel. accentColor tints the border and title; when
// empty it falls back to the primary color.
func Card(title, body string, width int, accentColor lipgloss.Color) string {
	accent := accentColor
	if accent == "" {
		accent = ColorPrimary
	}

	// Inner content width excludes the border (2 cells) and padding (2 cells).
	inner := width - 4
	if inner < 1 {
		inner = 1
	}

	header := lipgloss.NewStyle().Foreground(accent).Bold(true).Render(title)
	content := header
	if body != "" {
		content = header + "\n" + StyleBase.Render(body)
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Foreground(ColorText).
		Padding(0, 1).
		Width(inner).
		Render(content)
}

// StatPill renders a compact "label value" chip where the value is emphasized
// in the given color and the label is muted.
func StatPill(label, value string, color lipgloss.Color) string {
	c := color
	if c == "" {
		c = ColorPrimary
	}
	l := StyleMuted.Render(strings.TrimSpace(label))
	v := lipgloss.NewStyle().Foreground(c).Bold(true).Render(strings.TrimSpace(value))
	return l + " " + v
}

// KV renders an aligned "label  value" pair. The label is padded to a fixed
// column so successive KV lines align. Use kvWidth to tune the label column.
const kvWidth = 14

// KV renders a single aligned key/value row.
func KV(label, value string) string {
	l := runewidth.Truncate(label, kvWidth, "")
	l = runewidth.FillRight(l, kvWidth)
	key := StyleMuted.Render(l)
	val := StyleBase.Render(value)
	return key + " " + val
}

// Toast renders a transient feedback line colored by kind, prefixed with a
// glyph. It reuses BadgeKind semantics for coloring.
func Toast(msg string, kind BadgeKind) string {
	glyph := "•"
	switch kind {
	case KindHealthy, KindOnline:
		glyph = "✓"
	case KindSlow, KindCooldown:
		glyph = "!"
	case KindDead, KindOffline:
		glyph = "✕"
	}
	style := lipgloss.NewStyle().Foreground(badgeColor(kind))
	return style.Render(glyph + " " + strings.TrimSpace(msg))
}
