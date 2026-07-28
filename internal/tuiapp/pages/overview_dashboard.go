package pages

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"nlwproxy/internal/tuiapp/clipboard"
	"nlwproxy/internal/tuiapp/ui"
)

// OverviewDashboardData is the presentation-safe snapshot the redesigned
// Overview page renders. It carries no provider secrets — the local API key is
// always shown masked.
type OverviewDashboardData struct {
	Status  string
	BaseURL string
	// LocalKeyName is the environment variable name of the local gateway key
	// (e.g. "reffaunlimited_api_key"). The value itself is never shown.
	LocalKeyName string

	// Health composition.
	Healthy, Slow, Dead, Cooldown int

	// Traffic.
	Requests, Errors          int64
	InputTokens, OutputTokens int64
	RequestsPerMinute         float64
	TokensPerMinute           float64
	RequestSparkline          []float64
	TokenSparkline            []float64

	// Active exit.
	ExitCountry, ExitCity, ExitIP, ExitASN, ExitLatency string
}

const maskedKey = "•••"

// OverviewDashboard is the redesigned Overview page: Quick Connect, Health,
// Traffic and Active Exit cards. It is selection-safe and performs no idle
// redraw — it only reacts to copy keys and window resizes.
type OverviewDashboard struct {
	Data          OverviewDashboardData
	Clipboard     clipboard.Writer
	Message       string
	Width, Height int
}

// NewOverviewDashboard builds a dashboard with a default clipboard writer.
func NewOverviewDashboard() OverviewDashboard {
	return OverviewDashboard{Clipboard: clipboard.ClipEXE{}, Width: 100, Height: 30}
}

func (m *OverviewDashboard) SetSize(w, h int) { m.Width = max(1, w); m.Height = max(1, h) }
func (m OverviewDashboard) Init() tea.Cmd     { return nil }

// envExport returns the shell export block a caller copies with [e].
func (m OverviewDashboard) envExport() string {
	key := m.Data.LocalKeyName
	if key == "" {
		key = "reffaunlimited_api_key"
	}
	return fmt.Sprintf("export OPENAI_BASE_URL=%s\nexport OPENAI_API_KEY=$%s\n", m.Data.BaseURL, key)
}

// Update handles copy hotkeys and resize. It never schedules background work.
func (m OverviewDashboard) Update(msg tea.Msg) (OverviewDashboard, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
		return m, nil
	case tea.KeyMsg:
		var value, label string
		switch strings.ToLower(msg.String()) {
		case "c":
			value, label = m.Data.BaseURL, "Base URL"
		case "k":
			// The real key lives only in the environment; copy the shell
			// reference rather than a secret.
			key := m.Data.LocalKeyName
			if key == "" {
				key = "reffaunlimited_api_key"
			}
			value, label = "$"+key, "API key reference"
		case "e":
			value, label = m.envExport(), "env export"
		default:
			return m, nil
		}
		if m.Clipboard == nil {
			m.Message = "Clipboard unavailable"
			return m, nil
		}
		if err := m.Clipboard.WriteText(value); err != nil {
			m.Message = ui.Toast("Copy failed: "+err.Error(), ui.KindDead)
		} else {
			m.Message = ui.Toast("Copied "+label, ui.KindHealthy)
		}
	}
	return m, nil
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func (m OverviewDashboard) quickConnectCard(width int) string {
	key := m.Data.LocalKeyName
	if key == "" {
		key = "reffaunlimited_api_key"
	}
	body := strings.Join([]string{
		ui.KV("Base URL", orDash(m.Data.BaseURL)) + "  " + ui.StyleMuted.Render("[c] copy"),
		ui.KV("API key", key+`="`+maskedKey+`"`) + "  " + ui.StyleMuted.Render("[k] copy"),
		ui.KV("Env", "export OPENAI_API_KEY=$"+key) + "  " + ui.StyleMuted.Render("[e] copy"),
	}, "\n")
	return ui.Card("QUICK CONNECT", body, width, ui.ColorPrimary)
}

func (m OverviewDashboard) healthCard(width int) string {
	total := m.Data.Healthy + m.Data.Slow + m.Data.Dead + m.Data.Cooldown
	barW := max(8, width-6)
	bar := ui.SegmentBar([]ui.Segment{
		{Count: m.Data.Healthy, Kind: ui.KindHealthy},
		{Count: m.Data.Slow, Kind: ui.KindSlow},
		{Count: m.Data.Cooldown, Kind: ui.KindCooldown},
		{Count: m.Data.Dead, Kind: ui.KindDead},
	}, barW)
	pills := strings.Join([]string{
		ui.BadgeGlyph(ui.KindGlyph(ui.KindHealthy), fmt.Sprintf("%d healthy", m.Data.Healthy), ui.KindHealthy),
		ui.BadgeGlyph(ui.KindGlyph(ui.KindSlow), fmt.Sprintf("%d slow", m.Data.Slow), ui.KindSlow),
		ui.BadgeGlyph(ui.KindGlyph(ui.KindDead), fmt.Sprintf("%d dead", m.Data.Dead), ui.KindDead),
		ui.BadgeGlyph(ui.KindGlyph(ui.KindCooldown), fmt.Sprintf("%d cooldown", m.Data.Cooldown), ui.KindCooldown),
	}, "  ")
	body := bar + "\n" + pills + "\n" + ui.StyleMuted.Render(fmt.Sprintf("%d proxies tracked", total))
	return ui.Card("HEALTH", body, width, ui.ColorAccent)
}

func (m OverviewDashboard) trafficCard(width int) string {
	success := "100%"
	if m.Data.Requests > 0 {
		ok := m.Data.Requests - m.Data.Errors
		if ok < 0 {
			ok = 0
		}
		success = fmt.Sprintf("%.1f%%", float64(ok)/float64(m.Data.Requests)*100)
	}
	body := strings.Join([]string{
		ui.StatPill("REQUESTS", fmt.Sprintf("%d", m.Data.Requests), ui.ColorPrimary) + "   " +
			ui.StatPill("SUCCESS", success, ui.ColorPrimary) + "   " +
			ui.StatPill("ERRORS", fmt.Sprintf("%d", m.Data.Errors), ui.ColorDanger),
		ui.KV("RPM", fmt.Sprintf("%.1f", m.Data.RequestsPerMinute)) + "  " + ui.Sparkline(m.Data.RequestSparkline),
		ui.KV("Tokens", fmt.Sprintf("%d in / %d out", m.Data.InputTokens, m.Data.OutputTokens)),
		ui.KV("TPM", fmt.Sprintf("%.0f", m.Data.TokensPerMinute)) + "  " + ui.Sparkline(m.Data.TokenSparkline),
	}, "\n")
	return ui.Card("TRAFFIC", body, width, ui.ColorPrimary)
}

func (m OverviewDashboard) activeExitCard(width int) string {
	body := strings.Join([]string{
		ui.KV("Country", orDash(m.Data.ExitCountry)),
		ui.KV("City", orDash(m.Data.ExitCity)),
		ui.KV("IP", orDash(m.Data.ExitIP)),
		ui.KV("ASN", orDash(m.Data.ExitASN)),
		ui.KV("Latency", orDash(m.Data.ExitLatency)),
	}, "\n")
	return ui.Card("ACTIVE EXIT", body, width, ui.ColorAccent)
}

// View renders the four-card dashboard, responsively arranging cards into one
// or two columns based on available width.
func (m OverviewDashboard) View() string {
	w := max(1, m.Width)
	head := ui.StyleTitle.Render("Overview") + "  " + ui.StyleMuted.Render(strings.ToUpper(orDash(m.Data.Status)))

	var body string
	if w >= 88 {
		colW := (w - 2) / 2
		left := lipgloss.JoinVertical(lipgloss.Left, m.quickConnectCard(colW), m.trafficCard(colW))
		right := lipgloss.JoinVertical(lipgloss.Left, m.healthCard(colW), m.activeExitCard(colW))
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	} else {
		body = lipgloss.JoinVertical(lipgloss.Left,
			m.quickConnectCard(w), m.healthCard(w), m.trafficCard(w), m.activeExitCard(w))
	}

	rows := []string{head, "", body, "", ui.StyleMuted.Render("[c] copy URL  [k] copy key ref  [e] copy env export")}
	if m.Message != "" {
		rows = append(rows, "", m.Message)
	}
	return lipgloss.NewStyle().Width(w).Render(strings.Join(rows, "\n"))
}
