package pages

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"nlwproxy/internal/gateway"
	"nlwproxy/internal/metrics"
)

// OperationsSource is the presentation boundary for dashboard aggregators.
type OperationsSource interface{ Snapshot() OperationsSnapshot }

type OperationStats struct {
	Total, Errors, Active                               int64
	InputTokens, OutputTokens                           int64
	TTFTP50, TTFTP95, DurationP50, DurationP95, Latency time.Duration
	Health, Circuit                                     string
	CooldownUntil                                       time.Time
	Sparkline                                           []float64
}

type OperationsSnapshot struct {
	Revision                           uint64
	Status                             string
	Global                             OperationStats
	Models, Routes                     map[string]OperationStats
	Active, Recent                     []metrics.Request
	RequestsPerMinute, TokensPerMinute float64
	RequestSparkline, LatencySparkline []float64
}

func snapshot(source OperationsSource) OperationsSnapshot {
	if source == nil {
		return OperationsSnapshot{}
	}
	return source.Snapshot()
}

func metric(label, value string, width int) string {
	return lipgloss.NewStyle().Width(max(1, width)).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#30394B")).Padding(0, 1).Render(muted.Render(label) + "\n" + accent.Render(value))
}
func spark(values []float64) string {
	if len(values) == 0 {
		return "—"
	}
	blocks := []rune("▁▂▃▄▅▆▇█")
	lo, hi := values[0], values[0]
	for _, v := range values {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	var b strings.Builder
	for _, v := range values {
		n := 0
		if hi > lo {
			n = int((v - lo) / (hi - lo) * 7)
		}
		b.WriteRune(blocks[n])
	}
	return b.String()
}
func duration(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	return d.Round(time.Millisecond).String()
}
func tokenBar(in, out int64, width int) string {
	total := in + out
	if total <= 0 {
		return "—"
	}
	width = max(6, width)
	n := int(in * int64(width) / total)
	return good.Render(strings.Repeat("█", n)) + accent.Render(strings.Repeat("█", width-n))
}
func statusText(status int) string {
	s := fmtInt(status)
	switch {
	case status >= 200 && status < 300:
		return good.Render(s)
	case status >= 400:
		return bad.Render(s)
	default:
		return muted.Render(s)
	}
}

type OperationsOverview struct {
	Source        OperationsSource
	Width, Height int
}

func NewOperationsOverview(source OperationsSource) OperationsOverview {
	return OperationsOverview{Source: source, Width: 100, Height: 30}
}
func (m OperationsOverview) Init() tea.Cmd { return nil }
func (m OperationsOverview) Update(msg tea.Msg) (OperationsOverview, tea.Cmd) {
	if s, ok := msg.(tea.WindowSizeMsg); ok {
		m.Width = max(1, s.Width)
		m.Height = max(1, s.Height)
	}
	return m, nil
}
func (m OperationsOverview) View() string {
	s := snapshot(m.Source)
	w := max(1, m.Width)
	rows := []string{Title("Operations", strings.ToUpper(s.Status)), ""}
	if w < 60 {
		rows = append(rows, fmt.Sprintf("RPM %.1f  ERRORS %d  ACTIVE %d", s.RequestsPerMinute, s.Global.Errors, s.Global.Active), "TOKENS "+fmt.Sprintf("%d", s.Global.InputTokens+s.Global.OutputTokens), spark(s.RequestSparkline), "LATENCY "+spark(s.LatencySparkline))
	} else {
		cardW := max(12, (w-8)/4)
		cards := lipgloss.JoinHorizontal(lipgloss.Top, metric("REQUESTS", fmt.Sprintf("%d", s.Global.Total), cardW), metric("ERRORS", fmt.Sprintf("%d", s.Global.Errors), cardW), metric("ACTIVE", fmt.Sprintf("%d", s.Global.Active), cardW), metric("TOKENS", fmt.Sprintf("%d", s.Global.InputTokens+s.Global.OutputTokens), cardW))
		rows = append(rows, cards, "")
		traffic := "TRAFFIC\n" + fmt.Sprintf("%.1f rpm  %.0f tok/min\n%s", s.RequestsPerMinute, s.TokensPerMinute, spark(s.RequestSparkline))
		latency := "LATENCY\n" + fmt.Sprintf("P50 %s  P95 %s\n%s", duration(s.Global.DurationP50), duration(s.Global.DurationP95), spark(s.LatencySparkline))
		if w >= 130 {
			rows = append(rows, "TRAFFIC  &  LATENCY", lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(w/2-2).Render(traffic), lipgloss.NewStyle().Width(w/2-2).Render(latency)))
		} else {
			rows = append(rows, traffic, "", latency)
		}
	}
	return lipgloss.NewStyle().Width(w).Render(strings.Join(rows, "\n"))
}

type ModelsDetail struct {
	Models
	Source        OperationsSource
	Width, Height int
}

func NewModelsWithOperations(items []gateway.Model, selected string, source OperationsSource) ModelsDetail {
	return ModelsDetail{Models: NewModels(items, selected), Source: source, Width: 100}
}
func (m *ModelsDetail) SetSize(w, h int) { m.Width = max(1, w); m.Height = max(1, h) }
func (m ModelsDetail) Init() tea.Cmd     { return m.Models.Init() }
func (m ModelsDetail) Update(msg tea.Msg) (ModelsDetail, tea.Cmd) {
	if s, ok := msg.(tea.WindowSizeMsg); ok {
		m.SetSize(s.Width, s.Height)
	}
	var cmd tea.Cmd
	m.Models, cmd = m.Models.Update(msg)
	return m, cmd
}
func (m ModelsDetail) View() string {
	base := m.Models.View()
	s := snapshot(m.Source)
	st := s.Models[m.Selected]
	var meta gateway.Model
	for _, item := range m.All {
		if item.ID == m.Selected {
			meta = item
			break
		}
	}
	// Per-model traffic table: requests, tokens, TTFT, and a mini sparkline.
	table := []string{"", accent.Render("PER-MODEL TRAFFIC"), header.Render("MODEL          REQUESTS ERRORS  TOKENS   TTFT p50  TREND")}
	for _, item := range m.Filtered() {
		ms := s.Models[item.ID]
		trend := "—"
		if len(ms.Sparkline) > 0 {
			trend = spark(ms.Sparkline)
		}
		table = append(table, fmt.Sprintf("%-14s %8d %6d %7d   %-8s  %s",
			clip(item.ID, 14), ms.Total, ms.Errors, ms.InputTokens+ms.OutputTokens, duration(ms.TTFTP50), trend))
	}
	detail := []string{"", accent.Render("MODEL DETAIL"), fmt.Sprintf("ID       %s", m.Selected), fmt.Sprintf("OWNER    %s", meta.OwnedBy), fmt.Sprintf("REQUESTS %d  ERRORS %d", st.Total, st.Errors), fmt.Sprintf("TTFT p50 %s  p95 %s", duration(st.TTFTP50), duration(st.TTFTP95)), fmt.Sprintf("P50 %s  P95 %s", duration(st.DurationP50), duration(st.DurationP95)), fmt.Sprintf("INPUT %d  OUTPUT %d", st.InputTokens, st.OutputTokens), tokenBar(st.InputTokens, st.OutputTokens, min(30, max(6, m.Width/3)))}
	return lipgloss.NewStyle().Width(max(1, m.Width)).Render(base + "\n" + strings.Join(table, "\n") + "\n" + strings.Join(detail, "\n"))
}

type OperationsRequests struct {
	Source        OperationsSource
	Width, Height int
	ErrorsOnly    bool
	Sort          string
}

func NewOperationsRequests(source OperationsSource) OperationsRequests {
	return OperationsRequests{Source: source, Width: 100, Sort: "time"}
}
func (m *OperationsRequests) SetSize(w, h int) { m.Width = max(1, w); m.Height = max(1, h) }
func (m OperationsRequests) Init() tea.Cmd     { return nil }
func (m OperationsRequests) Update(msg tea.Msg) (OperationsRequests, tea.Cmd) {
	if s, ok := msg.(tea.WindowSizeMsg); ok {
		m.SetSize(s.Width, s.Height)
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		switch strings.ToLower(k.String()) {
		case "e":
			m.ErrorsOnly = !m.ErrorsOnly
		case "s":
			if m.Sort == "time" {
				m.Sort = "duration"
			} else {
				m.Sort = "time"
			}
		}
	}
	return m, nil
}
func (m OperationsRequests) events() []metrics.Request {
	items := append([]metrics.Request(nil), snapshot(m.Source).Recent...)
	if m.ErrorsOnly {
		out := items[:0]
		for _, e := range items {
			if e.Status >= 400 || e.ErrorCode != "" {
				out = append(out, e)
			}
		}
		items = out
	}
	sort.SliceStable(items, func(i, j int) bool {
		if m.Sort == "duration" {
			return items[i].Duration > items[j].Duration
		}
		return items[i].StartedAt.After(items[j].StartedAt)
	})
	return items
}
func (m OperationsRequests) View() string {
	filter := "all"
	if m.ErrorsOnly {
		filter = "errors"
	}
	rows := []string{Title("Requests", "metadata only — no prompt or response content"), fmt.Sprintf("FILTER %s  SORT %s  [e] errors  [s] sort", filter, m.Sort), header.Render("TIME     MODEL          PROXY       GEO          TOKENS   TTFT     LATENCY  STATUS")}
	for _, e := range m.events() {
		geo := e.ProxyCountry
		if geo == "" {
			geo = "—"
		}
		if e.ProxyCity != "" {
			geo += "/" + e.ProxyCity
		}
		rows = append(rows, fmt.Sprintf("%-8s %-14s %-11s %-12s %6d   %-8s %-8s %s",
			e.StartedAt.Format("15:04:05"),
			clip(e.RequestedModel, 14),
			clip(e.ProxyID, 11),
			clip(geo, 12),
			e.TotalTokens,
			duration(e.TTFT),
			duration(e.Duration),
			statusCell(e.Status, e.ErrorCode)))
	}
	if len(m.events()) == 0 {
		rows = append(rows, muted.Render("No matching requests."))
	}
	return lipgloss.NewStyle().Width(max(1, m.Width)).Render(strings.Join(rows, "\n"))
}

// statusCell colors an HTTP status plus an optional error code, metadata only.
func statusCell(status int, code string) string {
	cell := statusText(status)
	if code != "" {
		cell += " " + bad.Render(clip(code, 18))
	}
	return cell
}

type Logs struct {
	Source        OperationsSource
	Width, Height int
}

func NewLogs(source OperationsSource) Logs { return Logs{Source: source, Width: 100} }
func (m *Logs) SetSize(w, h int)           { m.Width = max(1, w); m.Height = max(1, h) }
func (m Logs) Init() tea.Cmd               { return nil }
func (m Logs) Update(msg tea.Msg) (Logs, tea.Cmd) {
	if s, ok := msg.(tea.WindowSizeMsg); ok {
		m.SetSize(s.Width, s.Height)
	}
	return m, nil
}
func (m Logs) View() string {
	rows := []string{Title("Logs", "operational lifecycle events"), header.Render("TIME     LEVEL  MODEL          PROXY       GEO          EVENT")}
	recent := snapshot(m.Source).Recent
	for i := len(recent) - 1; i >= 0; i-- {
		e := recent[i]
		level, event, styled := "INFO", "completed", good.Render("INFO ")
		switch {
		case e.Status >= 400 || e.ErrorCode != "":
			level, event = "ERROR", e.ErrorCode
			if event == "" {
				event = "request failed"
			}
			styled = bad.Render("ERROR")
		case e.RetryCount > 0:
			level, event = "WARN", fmt.Sprintf("retried x%d", e.RetryCount)
			styled = warn.Render("WARN ")
		}
		_ = level
		geo := e.ProxyCountry
		if geo == "" {
			geo = "—"
		}
		if e.ProxyCity != "" {
			geo += "/" + e.ProxyCity
		}
		rows = append(rows, fmt.Sprintf("%-8s %-6s %-14s %-11s %-12s %s", e.StartedAt.Format("15:04:05"), styled, clip(e.RequestedModel, 14), clip(e.ProxyID, 11), clip(geo, 12), event))
	}
	if len(recent) == 0 {
		rows = append(rows, muted.Render("No events yet."))
	}
	return lipgloss.NewStyle().Width(max(1, m.Width)).Render(strings.Join(rows, "\n"))
}

// OperationsRoutes renders per-route health, throughput and cooldown state from
// the operations snapshot. Now is injectable so cooldown countdowns are testable.
type OperationsRoutes struct {
	Source        OperationsSource
	Width, Height int
	Now           func() time.Time
}

func NewOperationsRoutes(source OperationsSource) OperationsRoutes {
	return OperationsRoutes{Source: source, Width: 100, Now: time.Now}
}
func (m *OperationsRoutes) SetSize(w, h int) { m.Width = max(1, w); m.Height = max(1, h) }
func (m OperationsRoutes) Init() tea.Cmd     { return nil }
func (m OperationsRoutes) Update(msg tea.Msg) (OperationsRoutes, tea.Cmd) {
	if s, ok := msg.(tea.WindowSizeMsg); ok {
		m.SetSize(s.Width, s.Height)
	}
	return m, nil
}

// routeBadge maps circuit + cooldown to a colored state label.
//
//	● ACTIVE   healthy, serving traffic
//	● READY    healthy, no live traffic
//	❄ COOLDOWN circuit open / cooling until CooldownUntil
func routeBadge(st OperationStats, now time.Time) string {
	cooling := !st.CooldownUntil.IsZero() && st.CooldownUntil.After(now)
	circuit := strings.ToLower(st.Circuit)
	switch {
	case cooling || circuit == "open":
		return warn.Render("❄ COOLDOWN")
	case st.Active > 0:
		return good.Render("● ACTIVE")
	default:
		return violet.Render("● READY")
	}
}

func errorPct(st OperationStats) string {
	if st.Total <= 0 {
		return "  0%"
	}
	return fmt.Sprintf("%3.0f%%", float64(st.Errors)*100/float64(st.Total))
}

func (m OperationsRoutes) View() string {
	now := time.Now()
	if m.Now != nil {
		now = m.Now()
	}
	s := snapshot(m.Source)
	names := make([]string, 0, len(s.Routes))
	for name := range s.Routes {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := []string{
		Title("Routes", "upstream health, throughput and cooldown"),
		header.Render("ROUTE             STATE        REQUESTS ERR%   LATENCY  COOLDOWN"),
	}
	for _, name := range names {
		st := s.Routes[name]
		cooldown := "—"
		if !st.CooldownUntil.IsZero() && st.CooldownUntil.After(now) {
			cooldown = warn.Render(compactDuration(st.CooldownUntil.Sub(now)))
		}
		latency := "—"
		if st.Latency > 0 {
			latency = st.Latency.Round(time.Millisecond).String()
		}
		errCol := errorPct(st)
		if st.Errors > 0 {
			errCol = bad.Render(errCol)
		}
		rows = append(rows, fmt.Sprintf("%-17s %-12s %8d %s %-8s %s",
			clip(name, 17), routeBadge(st, now), st.Total, errCol, latency, cooldown))
	}
	if len(names) == 0 {
		rows = append(rows, muted.Render("No active routes."))
	}
	return lipgloss.NewStyle().Width(max(1, m.Width)).Render(strings.Join(rows, "\n"))
}

// SettingsInfo is the presentation-safe view of runtime settings. It carries no
// secrets — only operator-facing knobs and a copyable connection block.
type SettingsInfo struct {
	ProxyOnly           bool
	ProxyOnlyLocked     bool
	RetryAttempts       int
	HealthCheckInterval time.Duration
	HonorRetryAfter     bool
	GeoCacheTTL         time.Duration
	ConnectionBlock     string
}

func DefaultSettingsInfo() SettingsInfo {
	return SettingsInfo{
		ProxyOnly:           true,
		ProxyOnlyLocked:     true,
		RetryAttempts:       12,
		HealthCheckInterval: 30 * time.Second,
		HonorRetryAfter:     true,
		GeoCacheTTL:         6 * time.Hour,
	}
}

// Settings renders a readable settings list plus a [copy] connection block.
type Settings struct {
	Info          SettingsInfo
	Width, Height int
	Copied        bool
}

func NewSettings(info SettingsInfo) Settings { return Settings{Info: info, Width: 100} }
func (m *Settings) SetSize(w, h int)         { m.Width = max(1, w); m.Height = max(1, h) }
func (m Settings) Init() tea.Cmd             { return nil }
func (m Settings) Update(msg tea.Msg) (Settings, tea.Cmd) {
	if s, ok := msg.(tea.WindowSizeMsg); ok {
		m.SetSize(s.Width, s.Height)
	}
	if k, ok := msg.(tea.KeyMsg); ok && strings.ToLower(k.String()) == "c" {
		m.Copied = true
	}
	return m, nil
}

func onOff(v bool) string {
	if v {
		return good.Render("ON")
	}
	return muted.Render("OFF")
}

func (m Settings) View() string {
	i := m.Info
	proxyLine := "Proxy-only            " + onOff(i.ProxyOnly)
	if i.ProxyOnlyLocked {
		proxyLine += muted.Render("  (locked for this profile)")
	}
	dur := func(d time.Duration) string {
		if d <= 0 {
			return "—"
		}
		return d.String()
	}
	rows := []string{
		Title("Settings", "runtime configuration"),
		"",
		accent.Render("CONNECTION"),
		proxyLine,
		"Retry attempts        " + textCol.Render(fmt.Sprintf("%d", i.RetryAttempts)),
		"Health check interval " + textCol.Render(dur(i.HealthCheckInterval)),
		"Honor Retry-After     " + onOff(i.HonorRetryAfter),
		"Geo cache TTL         " + textCol.Render(dur(i.GeoCacheTTL)),
	}
	if i.ProxyOnly && i.ProxyOnlyLocked {
		rows = append(rows, muted.Render("Direct connections are blocked."))
	}
	if strings.TrimSpace(i.ConnectionBlock) != "" {
		copyHint := violet.Render("[c] copy")
		if m.Copied {
			copyHint = good.Render("copied ✓")
		}
		block := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).BorderForeground(colAccent).
			Padding(0, 1).Foreground(colText).
			Render(i.ConnectionBlock)
		rows = append(rows, "", accent.Render("CLIENT CONNECTION")+"  "+copyHint, block)
	}
	return lipgloss.NewStyle().Width(max(1, m.Width)).Render(strings.Join(rows, "\n"))
}
