package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// stripANSI removes ANSI escape sequences so tests can assert on plain text
// regardless of the terminal color profile.
func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func TestBadge(t *testing.T) {
	kinds := []BadgeKind{KindHealthy, KindSlow, KindDead, KindCooldown, KindOnline, KindOffline}
	for _, k := range kinds {
		out := Badge("status", k)
		if out == "" {
			t.Fatalf("Badge(%q) returned empty", k)
		}
		plain := stripANSI(out)
		if !strings.Contains(plain, "STATUS") {
			t.Errorf("Badge(%q) = %q, want label STATUS", k, plain)
		}
		if !strings.Contains(plain, "●") {
			t.Errorf("Badge(%q) missing glyph: %q", k, plain)
		}
	}
}

func TestProgressBar(t *testing.T) {
	out := ProgressBar(124, 206, 40)
	if out == "" {
		t.Fatal("ProgressBar returned empty")
	}
	plain := stripANSI(out)
	if !strings.Contains(plain, "124/206") {
		t.Errorf("ProgressBar missing count: %q", plain)
	}
	if !strings.Contains(plain, "▓") || !strings.Contains(plain, "░") {
		t.Errorf("ProgressBar missing bar glyphs: %q", plain)
	}

	// Clamping: done > total should not panic and should show full/full.
	full := stripANSI(ProgressBar(300, 206, 40))
	if !strings.Contains(full, "206/206") {
		t.Errorf("ProgressBar clamp failed: %q", full)
	}

	// Zero total must not divide by zero.
	if got := ProgressBar(0, 0, 20); got == "" {
		t.Error("ProgressBar(0,0,20) returned empty")
	}
}

func TestSparkline(t *testing.T) {
	out := Sparkline([]float64{1, 3, 2, 8, 5, 0, 6})
	if out == "" {
		t.Fatal("Sparkline returned empty for non-empty input")
	}
	plain := stripANSI(out)
	if len([]rune(plain)) != 7 {
		t.Errorf("Sparkline width = %d, want 7", len([]rune(plain)))
	}
	if Sparkline(nil) != "" {
		t.Error("Sparkline(nil) should be empty")
	}
	// Flat series must not panic (span == 0).
	if got := Sparkline([]float64{5, 5, 5}); stripANSI(got) == "" {
		t.Error("Sparkline flat series returned empty")
	}
}

func TestCard(t *testing.T) {
	out := Card("METRICS", "line one\nline two", 30, ColorAccent)
	if out == "" {
		t.Fatal("Card returned empty")
	}
	plain := stripANSI(out)
	if !strings.Contains(plain, "METRICS") {
		t.Errorf("Card missing title: %q", plain)
	}
	if !strings.Contains(plain, "line one") {
		t.Errorf("Card missing body: %q", plain)
	}
	// Empty accent falls back to primary without panic.
	if got := Card("T", "", 20, lipgloss.Color("")); got == "" {
		t.Error("Card with empty accent returned empty")
	}
}

func TestStatPill(t *testing.T) {
	out := StatPill("RPM", "1240", ColorPrimary)
	if out == "" {
		t.Fatal("StatPill returned empty")
	}
	plain := stripANSI(out)
	if !strings.Contains(plain, "RPM") || !strings.Contains(plain, "1240") {
		t.Errorf("StatPill missing label/value: %q", plain)
	}
}

func TestKV(t *testing.T) {
	out := KV("Latency", "42ms")
	if out == "" {
		t.Fatal("KV returned empty")
	}
	plain := stripANSI(out)
	if !strings.Contains(plain, "Latency") || !strings.Contains(plain, "42ms") {
		t.Errorf("KV missing label/value: %q", plain)
	}
	// Alignment: label column is padded to fixed width.
	if !strings.Contains(plain, "Latency ") {
		t.Errorf("KV not aligned: %q", plain)
	}
}

func TestToast(t *testing.T) {
	cases := map[BadgeKind]string{
		KindHealthy:  "✓",
		KindSlow:     "!",
		KindDead:     "✕",
		KindOnline:   "✓",
		KindCooldown: "!",
		KindOffline:  "✕",
	}
	for kind, glyph := range cases {
		out := Toast("saved", kind)
		if out == "" {
			t.Fatalf("Toast(%q) returned empty", kind)
		}
		plain := stripANSI(out)
		if !strings.Contains(plain, "saved") {
			t.Errorf("Toast(%q) missing msg: %q", kind, plain)
		}
		if !strings.Contains(plain, glyph) {
			t.Errorf("Toast(%q) glyph = %q, want %q", kind, plain, glyph)
		}
	}
}

func TestThemeConstants(t *testing.T) {
	// Guard against accidental palette drift.
	if ColorPrimary != lipgloss.Color("#5EEAD4") {
		t.Errorf("ColorPrimary = %q", ColorPrimary)
	}
	if ColorDanger != lipgloss.Color("#FB7185") {
		t.Errorf("ColorDanger = %q", ColorDanger)
	}
}
