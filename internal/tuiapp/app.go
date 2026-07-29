// Package tuiapp provides the Bubble Tea application shell for NlwProxy.
package tuiapp

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"nlwproxy/internal/tuiapp/clipboard"
	"nlwproxy/internal/tuiapp/pages"
)

// Page identifies a top-level application destination.
type Page int

const (
	PageOverview Page = iota
	PageModels
	PageProxies
	PageRoutes
	PageRequests
	PageLogs
	PageProfiles
	PageSettings
)

func (p Page) String() string {
	switch p {
	case PageOverview:
		return "Overview"
	case PageModels:
		return "Models"
	case PageProxies:
		return "Proxies"
	case PageRoutes:
		return "Routes"
	case PageRequests:
		return "Requests"
	case PageLogs:
		return "Logs"
	case PageProfiles:
		return "Profiles"

	case PageSettings:
		return "Settings"
	default:
		return "Unknown"
	}
}

var navigationPages = []Page{PageOverview, PageModels, PageProxies, PageRoutes, PageRequests, PageLogs, PageProfiles, PageSettings}

type focusArea uint8

const (
	focusSidebar focusArea = iota
	focusContent
)

// Snapshot is the presentation-safe state consumed by the shell. It deliberately
// contains no request/response content or provider secrets.
type Snapshot struct {
	Profile        string
	Gateway        string
	Status         string
	Notice         string
	Requests       int64
	Errors         int64
	Active         int64
	Connections    int
	Models         int
	InputTokens    int64
	OutputTokens   int64
	Routes         []Route
	Events         []Event
	ProxyOnly      bool
	HealthyProxies int
	ActiveProxy    string
	ProxyCountry   string
}

type Route struct {
	Name, Health, Circuit, Transport            string
	Requests, Errors, InputTokens, OutputTokens int64
}

type Event struct {
	RequestID, RouteID, Model, Endpoint, ErrorCode, State string
	Status, RetryCount                                    int
	InputTokens, OutputTokens, TotalTokens                int64
	StartedAt                                             time.Time
	TTFT, Duration                                        time.Duration
}

// StateSource bridges gateway/profile state into Bubble Tea without polling.
// Changes returns a channel that closes when a newer snapshot is available.
type StateSource interface {
	Snapshot() Snapshot
	Changes() <-chan struct{}
}

type stateChangedMsg struct{ snapshot Snapshot }
type sourceClosedMsg struct{}

// Notify returns an event-driven command for a source. Bubble Tea executes the
// blocking wait outside Update and the model immediately re-arms it after each
// change. A nil source or channel schedules no command and causes no idle redraw.
func Notify(ctx context.Context, source StateSource) tea.Cmd {
	if source == nil {
		return nil
	}
	changes := source.Changes()
	if changes == nil {
		return nil
	}
	return func() tea.Msg {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-changes:
			if !ok {
				return sourceClosedMsg{}
			}
			return stateChangedMsg{snapshot: source.Snapshot()}
		}
	}
}

type keyMap struct {
	Up, Down key.Binding
	Select   key.Binding
	Freeze   key.Binding
	Quit     key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		Up:     key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:   key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Select: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
		Freeze: key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "freeze")),
		Quit:   key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Select, k.Freeze, k.Quit}
}
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Up, k.Down, k.Select, k.Freeze, k.Quit}}
}

// Theme centralizes all shell colors and reusable styles.
type Theme struct {
	Background lipgloss.Color
	Surface    lipgloss.Color
	Border     lipgloss.Color
	Primary    lipgloss.Color
	Accent     lipgloss.Color
	Text       lipgloss.Color
	Muted      lipgloss.Color
	Success    lipgloss.Color
	Warning    lipgloss.Color
	Danger     lipgloss.Color
}

func DefaultTheme() Theme {
	return Theme{
		Background: lipgloss.Color("#0B0D12"),
		Surface:    lipgloss.Color("#141821"),
		Border:     lipgloss.Color("#293041"),
		Primary:    lipgloss.Color("#72E1D1"),
		Accent:     lipgloss.Color("#9A8CFF"),
		Text:       lipgloss.Color("#F3F5F7"),
		Muted:      lipgloss.Color("#8992A5"),
		Success:    lipgloss.Color("#70D69A"),
		Warning:    lipgloss.Color("#F5C76B"),
		Danger:     lipgloss.Color("#FF7D8A"),
	}
}

// Model is the top-level Bubble Tea model.
type Model struct {
	ctx          context.Context
	source       StateSource
	lifecycle    Lifecycle
	operations   pages.OperationsSource
	overview     pages.OperationsOverview
	overviewDash pages.OverviewDashboard
	modelsPage   pages.ModelsDetail
	proxiesPage  pages.ProxiesPage
	requestsPage pages.OperationsRequests
	routesPage   pages.OperationsRoutes
	logsPage     pages.Logs
	settingsPage pages.Settings
	theme        Theme
	keys         keyMap
	help         help.Model
	selected     int
	active       Page
	focus        focusArea
	width        int
	height       int
	ready        bool
	frozen       bool
	frozenView   string
	snapshot     Snapshot
}

func New(ctx context.Context, source StateSource) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	m := Model{ctx: ctx, source: source, theme: DefaultTheme(), keys: newKeyMap(), help: help.New(), active: PageOverview, overviewDash: pages.NewOverviewDashboard()}
	if source != nil {
		m.snapshot = source.Snapshot()
	}
	return m
}

func (m Model) Init() tea.Cmd {
	if m.lifecycle != nil {
		return startRuntime(m.ctx, m.lifecycle)
	}
	return Notify(m.ctx, m.source)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = max(1, msg.Width)
		m.height = max(1, msg.Height)
		m.help.Width = m.width
		m.ready = true
		contentW, contentH := m.contentSize()
		m.overview.Width, m.overview.Height = contentW, contentH
		m.overviewDash.SetSize(contentW, contentH)
		m.refreshOverviewDash()
		m.modelsPage.SetSize(contentW, contentH)
		m.proxiesPage, _ = m.proxiesPage.Update(tea.WindowSizeMsg{Width: contentW, Height: contentH})
		m.requestsPage.SetSize(contentW, contentH)
		m.routesPage.SetSize(contentW, contentH)
		m.logsPage.SetSize(contentW, contentH)
		m.settingsPage.SetSize(contentW, contentH)
	case tea.KeyMsg:
		if m.frozen && !key.Matches(msg, m.keys.Freeze) && !key.Matches(msg, m.keys.Quit) {
			return m, nil
		}
		if key.Matches(msg, m.keys.Freeze) {
			if m.frozen {
				m.frozen, m.frozenView = false, ""
				return m, Notify(m.ctx, m.source)
			}
			m.frozen = true
			return m, nil
		}
		switch {
		case key.Matches(msg, m.keys.Quit):
			if m.lifecycle != nil {
				m.snapshot.Status = "STOPPING"
				return m, stopRuntime(m.lifecycle)
			}
			return m, tea.Quit
		case msg.Type == tea.KeyTab:
			if m.focus == focusSidebar {
				m.enterContent()
			} else {
				m.focus = focusSidebar
			}
		case m.focus == focusContent && (msg.Type == tea.KeyEsc || msg.Type == tea.KeyLeft):
			m.focus = focusSidebar
		case m.focus == focusSidebar && (key.Matches(msg, m.keys.Select) || msg.Type == tea.KeyRight):
			m.enterContent()
		case m.focus == focusSidebar && key.Matches(msg, m.keys.Up):
			m.selected = (m.selected - 1 + len(navigationPages)) % len(navigationPages)
		case m.focus == focusSidebar && key.Matches(msg, m.keys.Down):
			m.selected = (m.selected + 1) % len(navigationPages)
		case m.focus == focusContent:
			return m, m.updateContent(msg)
		}
	case tea.MouseMsg:
		if m.frozen {
			return m, nil
		}
		if page, ok := m.sidebarPageAt(msg.X, msg.Y); ok && msg.Type == tea.MouseLeft {
			m.selected, m.active, m.focus = int(page), page, focusContent
			return m, nil
		}
		if m.operations != nil && m.active == PageProxies {
			if msg.X >= m.contentOriginX() {
				msg.X -= m.contentOriginX() + 2
				msg.Y--
			}
			m.proxiesPage, _ = m.proxiesPage.Update(msg)
		}
	case stateChangedMsg:
		if m.frozen {
			return m, nil
		}
		m.snapshot = msg.snapshot
		if m.operations != nil {
			m.overview.Source = m.operations
			m.modelsPage.Source = m.operations
			m.requestsPage.Source = m.operations
			m.routesPage.Source = m.operations
			m.logsPage.Source = m.operations
		}
		m.refreshOverviewDash()
		m.refreshSettings()
		return m, Notify(m.ctx, m.source)
	case sourceClosedMsg:
		m.source = nil
	case runtimeStartedMsg:
		if msg.err != nil {
			m.snapshot = lifecycleSnapshot(m.snapshot, "FAILED", msg.err)
			return m, nil
		}
		m.snapshot = lifecycleSnapshot(m.source.Snapshot(), "ONLINE", nil)
		return m, Notify(m.ctx, m.source)
	case runtimeStoppedMsg:
		if msg.err != nil {
			m.snapshot = lifecycleSnapshot(m.snapshot, "FAILED", msg.err)
			return m, nil
		}
		return m, tea.Quit
	default:
		if m.active == PageProxies {
			m.proxiesPage, _ = m.proxiesPage.Update(msg)
			m.refreshOverviewDash()
		}
	}
	return m, nil
}

func (m Model) View() string {
	if !m.ready {
		return ""
	}
	if m.frozen && m.frozenView != "" {
		return m.frozenView
	}

	width, height := max(1, m.width), max(1, m.height)
	statusHeight := 1
	bodyHeight := max(1, height-statusHeight)
	compact := width < 72

	var body string
	if compact {
		body = m.compactBody(width, bodyHeight)
	} else {
		sidebarWidth := clamp(width/4, 22, 30)
		contentWidth := max(1, width-sidebarWidth)
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			m.sidebar(sidebarWidth, bodyHeight),
			m.content(contentWidth, bodyHeight),
		)
	}
	status := m.statusBar(width)
	view := lipgloss.NewStyle().Background(m.theme.Background).Width(width).Height(height).MaxHeight(height).Render(
		lipgloss.JoinVertical(lipgloss.Left, body, status),
	)
	if m.frozen {
		m.frozenView = view
	}
	return view
}

func (m *Model) enterContent() {
	m.active = navigationPages[m.selected]
	m.focus = focusContent
	if m.active == PageSettings {
		m.refreshSettings()
	}
	if m.active == PageOverview {
		m.refreshOverviewDash()
	}
}

// copyToClipboard returns a command that writes text to the system clipboard
// via clip.exe (Windows) without blocking the UI thread.
func copyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		_ = clipboard.ClipEXE{}.WriteText(text)
		return nil
	}
}

func (m *Model) updateContent(msg tea.KeyMsg) tea.Cmd {
	if m.active == PageOverview {
		var cmd tea.Cmd
		m.overviewDash, cmd = m.overviewDash.Update(msg)
		return cmd
	}
	// Settings is available in proxy-only mode without a live operations source.
	if m.active == PageSettings {
		var cmd tea.Cmd
		m.settingsPage, cmd = m.settingsPage.Update(msg)
		if m.settingsPage.Copied {
			cmd = tea.Batch(cmd, copyToClipboard(m.settingsPage.Info.ConnectionBlock))
		}
		return cmd
	}
	if m.operations == nil {
		return nil
	}
	var cmd tea.Cmd
	switch m.active {
	case PageModels:
		m.modelsPage, cmd = m.modelsPage.Update(msg)
	case PageProxies:
		m.proxiesPage, cmd = m.proxiesPage.Update(msg)
	case PageRequests:
		m.requestsPage, cmd = m.requestsPage.Update(msg)
	case PageRoutes:
		m.routesPage, cmd = m.routesPage.Update(msg)
	case PageLogs:
		m.logsPage, _ = m.logsPage.Update(msg)
	}
	return cmd
}

func (m Model) contentOriginX() int {
	if m.width < 72 {
		return 0
	}
	return clamp(m.width/4, 22, 30)
}

func (m Model) sidebarPageAt(x, y int) (Page, bool) {
	if m.width < 72 || x < 0 || x >= m.contentOriginX() {
		return 0, false
	}
	const firstItemY = 4
	index := y - firstItemY
	if index < 0 || index >= len(navigationPages) {
		return 0, false
	}
	return navigationPages[index], true
}

func (m Model) sidebar(width, height int) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Primary).Render("NLWPROXY")
	subtitle := lipgloss.NewStyle().Foreground(m.theme.Muted).Render("CONTROL PLANE")
	items := make([]string, 0, len(navigationPages))
	for i, page := range navigationPages {
		marker := "  "
		style := lipgloss.NewStyle().Foreground(m.theme.Muted)
		if i == m.selected {
			marker = "› "
			style = style.Foreground(m.theme.Text).Bold(true).Background(m.theme.Border)
		}
		active := ""
		if page == m.active {
			active = " •"
		}
		items = append(items, style.Width(max(1, width-4)).Render(marker+page.String()+active))
	}
	profile := m.snapshot.Profile
	if profile == "" {
		profile = "default"
	}
	footer := lipgloss.NewStyle().Foreground(m.theme.Muted).Render("PROFILE\n" + profile)
	content := lipgloss.JoinVertical(lipgloss.Left, title, subtitle, "", strings.Join(items, "\n"))
	spacer := strings.Repeat("\n", max(0, height-lipgloss.Height(content)-lipgloss.Height(footer)-4))
	return lipgloss.NewStyle().
		Width(width).Height(height).
		Padding(1, 2).
		Background(m.theme.Surface).
		BorderRight(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(m.theme.Border).
		Render(content + spacer + "\n" + footer)
}

func (m Model) contentSize() (int, int) {
	width, height := max(1, m.width), max(1, m.height-1)
	if width < 72 {
		return max(1, width-4), max(1, height-4)
	}
	sidebar := clamp(width/4, 22, 30)
	return max(1, width-sidebar-6), max(1, height-4)
}

// refreshOverviewDash rebuilds the redesigned Overview dashboard data from the
// current snapshot and operations aggregator. It reads presentation-safe
// fields only and never touches routing/gateway/proxy-loader logic.
func (m *Model) refreshOverviewDash() {
	data := pages.OverviewDashboardData{
		Status:       m.snapshot.Status,
		BaseURL:      m.snapshot.Gateway,
		LocalKeyName: "reffaunlimited_api_key",
		Requests:     m.snapshot.Requests,
		Errors:       m.snapshot.Errors,
		InputTokens:  m.snapshot.InputTokens,
		OutputTokens: m.snapshot.OutputTokens,
		ExitCountry:  m.snapshot.ProxyCountry,
		ActiveRoutes: m.snapshot.Connections,
	}
	if m.snapshot.ActiveProxy != "" {
		data.ExitIP = m.snapshot.ActiveProxy
	}
	// Health composition: derive from proxy stats where available, falling
	// back to the healthy count carried on the snapshot.
	data.Healthy = m.snapshot.HealthyProxies
	if src := m.proxiesPage.Source; src != nil {
		stats := src.Stats()
		data.Total = stats.Total
		data.Healthy = stats.Healthy
		data.Slow = stats.Slow
		data.Dead = stats.Dead
	}
	if m.operations != nil {
		s := m.operations.Snapshot()
		data.RequestsPerMinute = s.RequestsPerMinute
		data.TokensPerMinute = s.TokensPerMinute
		data.RequestSparkline = s.RequestSparkline
		data.TokenSparkline = s.LatencySparkline
		if data.Requests == 0 {
			data.Requests = s.Global.Total
		}
		if data.Errors == 0 {
			data.Errors = s.Global.Errors
		}
	}
	m.overviewDash.Data = data
}

// refreshSettings rebuilds the presentation-safe Settings view from the current
// snapshot. Proxy-only is treated as locked ON for this profile; the connection
// block is a copyable client hint built from the gateway address only.
func (m *Model) refreshSettings() {
	info := pages.DefaultSettingsInfo()
	info.ProxyOnly = m.snapshot.ProxyOnly
	info.ProxyOnlyLocked = m.snapshot.ProxyOnly
	base := m.snapshot.Gateway
	if base == "" {
		base = "127.0.0.1:8787"
	}
	info.ConnectionBlock = fmt.Sprintf("base_url  http://%s/v1\napi_key   reffaunlimited_api_key", base)
	m.settingsPage.Info = info
}

func (m Model) operationalView() string {
	if m.operations == nil && m.active != PageOverview && m.active != PageSettings {
		return ""
	}
	switch m.active {
	case PageOverview:
		view := m.overviewDash.View()
		if m.snapshot.ProxyOnly {
			status := "PROXY ONLY  •  DIRECT BLOCKED"
			if m.snapshot.Connections == 0 {
				status += "  •  NO ACTIVE PROXY ROUTES"
			}
			view = status + "\n\n" + view
		}
		return view
	case PageModels:
		return m.modelsPage.View()
	case PageProxies:
		return m.proxiesPage.View()
	case PageRequests:
		return m.requestsPage.View()
	case PageLogs:
		return m.logsPage.View()
	case PageRoutes:
		return m.routesPage.View()
	case PageSettings:
		return m.settingsPage.View()
	}
	return ""
}

func (m Model) content(width, height int) string {
	if operational := m.operationalView(); operational != "" {
		return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Padding(1, 2).Background(m.theme.Background).Render(operational)
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Text).Render(m.active.String())
	description := pageDescription(m.active)
	state := m.snapshot.Status
	if state == "" {
		state = "Not connected"
	}
	cards := fmt.Sprintf("Gateway      %s\nConnections  %d\nModels       %d\nRequests     %d\nErrors       %d\nActive       %d\nTokens       %d in / %d out",
		state, m.snapshot.Connections, m.snapshot.Models, m.snapshot.Requests, m.snapshot.Errors, m.snapshot.Active, m.snapshot.InputTokens, m.snapshot.OutputTokens)
	placeholder := lipgloss.NewStyle().
		MarginTop(1).Padding(1, 2).
		Border(lipgloss.RoundedBorder()).BorderForeground(m.theme.Border).
		Foreground(m.theme.Muted).Width(max(1, width-8)).
		Render(cards + "\n\n" + description + "\n\nPage implementation follows in the next delivery slice.")
	if m.snapshot.Notice != "" {
		placeholder += "\n" + lipgloss.NewStyle().Foreground(m.theme.Accent).Render(m.snapshot.Notice)
	}
	return lipgloss.NewStyle().Width(width).Height(height).Padding(1, 3).Background(m.theme.Background).Render(title + "\n" + placeholder)
}

func (m Model) compactBody(width, height int) string {
	nav := fmt.Sprintf("%s  ·  %s", m.active.String(), navigationPages[m.selected].String())
	header := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Primary).Width(width).Padding(0, 1).Render("NLWPROXY  /  " + nav)
	return lipgloss.NewStyle().Width(width).Height(height).Padding(1).Background(m.theme.Background).Render(
		header + "\n\n" + m.content(max(1, width-2), max(1, height-2)),
	)
}

func (m Model) statusBar(width int) string {
	left := m.snapshot.Gateway
	if left == "" {
		left = "gateway offline"
	}
	right := m.help.View(m.keys) + " • Shift+drag select • Ctrl+Shift+C copy"
	if m.frozen {
		left += " • FROZEN"
	}
	gap := max(1, width-lipgloss.Width(left)-lipgloss.Width(right)-2)
	return lipgloss.NewStyle().Width(width).Foreground(m.theme.Muted).Background(m.theme.Surface).Render(" " + left + strings.Repeat(" ", gap) + right)
}

func pageDescription(page Page) string {
	switch page {
	case PageOverview:
		return "Gateway health and high-level usage."
	case PageModels:
		return "Model traffic, token mix, and latency detail."
	case PageProxies:
		return "Imported proxy list, batch test, and geo lookup."
	case PageRoutes:
		return "Authorized upstream routes and transport state."
	case PageRequests:
		return "Filterable and sortable metadata-only request activity."
	case PageLogs:
		return "Operational lifecycle events and failures."
	case PageProfiles:
		return "Saved runtime profiles and active selection."

	case PageSettings:
		return "Gateway and client integration settings."
	default:
		return ""
	}
}

func clamp(value, low, high int) int { return min(max(value, low), high) }

func (m Model) DebugView(width, height int) string {
	m.width, m.height, m.ready = width, height, true
	if m.operations != nil {
		m.overview.Width, m.overview.Height = width, height
	}
	return m.View()
}

// Run launches the TUI with cell mouse reporting. Hold Shift while dragging
// to preserve the terminal's native text selection.
func Run(ctx context.Context, in io.Reader, out io.Writer, source StateSource) error {
	options := []tea.ProgramOption{tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx)}
	if in != nil {
		options = append(options, tea.WithInput(in))
	}
	if out != nil {
		options = append(options, tea.WithOutput(out))
	}
	_, err := tea.NewProgram(New(ctx, source), options...).Run()
	return err
}

// RunRuntime launches the TUI with a gateway owned by the program lifecycle.
func RunRuntime(ctx context.Context, in io.Reader, out io.Writer, lifecycle Lifecycle) error {
	options := []tea.ProgramOption{tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx)}
	if in != nil {
		options = append(options, tea.WithInput(in))
	}
	if out != nil {
		options = append(options, tea.WithOutput(out))
	}
	_, err := tea.NewProgram(NewRuntime(ctx, lifecycle), options...).Run()
	return err
}
