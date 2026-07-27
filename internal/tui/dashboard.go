package tui

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	reset   = "\x1b[0m"
	bold    = "\x1b[1m"
	dim     = "\x1b[2m"
	cyan    = "\x1b[36m"
	green   = "\x1b[32m"
	yellow  = "\x1b[33m"
	red     = "\x1b[31m"
	magenta = "\x1b[35m"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

type Route struct {
	Name, Transport, State string
	Priority               int
	Requests, Active       int64
	Latency                time.Duration
	Score                  int
}

type Snapshot struct {
	Version, Listen, Strategy string
	Started                   time.Time
	Requests, Errors, Active  int64
	Routes                    []Route
	LastError                 string
}

type Dashboard struct {
	Out   io.Writer
	Color bool
	Width int
}

func New(out io.Writer) Dashboard { return Dashboard{Out: out, Color: supportsColor(out), Width: 92} }

func supportsColor(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb" && fileIsTerminal(f)
}
func fileIsTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
func (d Dashboard) paint(code, text string) string {
	if !d.Color {
		return text
	}
	return code + text + reset
}
func visibleLen(s string) int { return utf8.RuneCountInString(ansiPattern.ReplaceAllString(s, "")) }
func pad(s string, n int) string {
	if visibleLen(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-visibleLen(s))
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

func (d Dashboard) Render(s Snapshot) string {
	width := d.Width
	if width < 78 {
		width = 78
	}
	if width > 140 {
		width = 140
	}
	line := strings.Repeat("─", width-2)
	var b strings.Builder
	row := func(content string) { fmt.Fprintf(&b, "│%s│\n", pad(content, width-2)) }
	section := func(title string) {
		label := " " + strings.ToUpper(title) + " "
		rest := width - 2 - utf8.RuneCountInString(label)
		fmt.Fprintf(&b, "├%s%s┤\n", d.paint(dim, label), strings.Repeat("─", max(0, rest)))
	}

	fmt.Fprintf(&b, "┌%s┐\n", line)
	row(d.paint(bold+cyan, "  NLWPROXY") + d.paint(dim, "  /  OPENCODE NETWORK CONTROL") + strings.Repeat(" ", 2) + d.paint(dim, "v"+s.Version))
	fmt.Fprintf(&b, "├%s┤\n", line)
	state := d.paint(yellow, "○ STOPPED")
	uptime := "—"
	if !s.Started.IsZero() {
		state = d.paint(green, "● ONLINE")
		uptime = time.Since(s.Started).Round(time.Second).String()
	}
	enabled, degraded := 0, 0
	for _, route := range s.Routes {
		if !strings.EqualFold(route.State, "disabled") {
			enabled++
		}
		if strings.EqualFold(route.State, "degraded") || strings.EqualFold(route.State, "open") {
			degraded++
		}
	}
	row(fmt.Sprintf("  %-18s  LISTEN  %-22s  UPTIME  %-12s", state, clip(s.Listen, 22), uptime))
	row(fmt.Sprintf("  STRATEGY  %-12s  ROUTES %d/%d enabled  ACTIVE %-4d  ERRORS %d", clip(s.Strategy, 12), enabled, len(s.Routes), s.Active, s.Errors))

	section("Connections")
	nameW := max(12, width-68)
	header := fmt.Sprintf("  %-*s  %-10s  %-15s  %4s  %5s  %7s  %6s", nameW, "ROUTE", "TRANSPORT", "STATE", "PRIO", "SCORE", "ACTIVE", "LATENCY")
	row(d.paint(bold, header))
	row(d.paint(dim, "  "+strings.Repeat("·", width-6)))
	if len(s.Routes) == 0 {
		row(d.paint(dim, "  No routes configured. Run: nlwproxy proxy add <name> --base-url <https-url>"))
	}
	for _, r := range s.Routes {
		symbol, color := stateStyle(r.State)
		latency := "—"
		if r.Latency > 0 {
			latency = r.Latency.Round(time.Millisecond).String()
		}
		score := "—"
		if r.Score > 0 {
			score = fmt.Sprint(r.Score)
		}
		stateText := d.paint(color, symbol+" "+strings.ToUpper(clip(r.State, 12)))
		row(fmt.Sprintf("  %-*s  %-10s  %-15s  %4d  %5s  %7d  %6s", nameW, clip(r.Name, nameW), clip(r.Transport, 10), stateText, r.Priority, score, r.Active, latency))
	}

	section("Activity")
	success := s.Requests - s.Errors
	if success < 0 {
		success = 0
	}
	row(fmt.Sprintf("  REQUESTS  %-10d  SUCCESS  %-10d  FAILURES  %-10d", s.Requests, success, s.Errors))
	if s.LastError != "" {
		row(d.paint(red, "  LAST ERROR  ") + clip(s.LastError, width-16))
	} else {
		row(d.paint(dim, "  No request content is stored. Operational metadata only."))
	}

	section("Keys")
	row(d.paint(dim, "  [R] refresh   [C] connections   [H] health   [D] diagnostics   [Q] quit"))
	row(d.paint(dim, "  ● healthy   ◐ degraded   ○ open/unknown   × auth required   — disabled"))
	fmt.Fprintf(&b, "└%s┘\n", line)
	if degraded > 0 {
		fmt.Fprintf(&b, "%s\n", d.paint(yellow, fmt.Sprintf("Attention: %d route(s) degraded or open.", degraded)))
	}
	return b.String()
}

func stateStyle(state string) (string, string) {
	switch strings.ToLower(state) {
	case "healthy", "ready":
		return "●", green
	case "degraded":
		return "◐", yellow
	case "auth_required", "auth-required":
		return "×", red
	case "disabled":
		return "—", dim
	case "open", "unhealthy":
		return "○", red
	default:
		return "○", magenta
	}
}

func (d Dashboard) Draw(s Snapshot) error { _, err := io.WriteString(d.Out, d.Render(s)); return err }
