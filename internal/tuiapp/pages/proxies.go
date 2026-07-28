package pages

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"nlwproxy/internal/proxymanager"
	"nlwproxy/internal/tuiapp/clipboard"
	"nlwproxy/internal/tuiapp/ui"
)

type ProxyManagerSource interface {
	List() []proxymanager.ProxyEntry
	Count() (total, alive int)
	Stats() proxymanager.Stats
	ImportFile(path string) (int, []string)
	TestSingle(ctx context.Context, id, testURL string) proxymanager.TestResult
	TestAll(ctx context.Context, testURL string) []proxymanager.TestResult
	Get(id string) (proxymanager.ProxyEntry, bool)
	Remove(id string) bool
	Reload(context.Context) error
}

type testResultMsg struct{ Result proxymanager.TestResult }
type testAllResultMsg struct{ Results []proxymanager.TestResult }
type importResultMsg struct {
	Added  int
	Errors []string
}

type ProxyStatus string

const (
	ProxyStatusAll   ProxyStatus = "all"
	ProxyStatusAlive ProxyStatus = "alive"
	ProxyStatusDead  ProxyStatus = "dead"
)

type ProxySort string

const (
	ProxySortDefault ProxySort = "id"
	ProxySortLatency ProxySort = "latency"
	ProxySortType    ProxySort = "type"
	ProxySortCountry ProxySort = "country"
)

// ProxiesPage displays and locally filters imported proxy metadata. It never scrapes.
type ProxiesPage struct {
	Source                ProxyManagerSource
	Clipboard             clipboard.Writer
	Cursor                int
	Offset                int
	Message, Status       string
	Width, Height         int
	Query                 string
	Searching             bool
	StatusFilter          ProxyStatus
	TypeFilter            proxymanager.ProxyScheme
	CountryFilter         string
	LatencyFilter         time.Duration
	Sort                  ProxySort
	Selected              map[string]bool
	importing, testing    bool
	testURL, searchBuffer string
	importBuffer          string
	ctx                   context.Context
	testTotal, testDone   int
	testHealthy, testSlow int
	testDead              int
}

func NewProxiesPage(source ProxyManagerSource) ProxiesPage {
	return ProxiesPage{Source: source, Clipboard: clipboard.ClipEXE{}, StatusFilter: ProxyStatusAll, Sort: ProxySortDefault, Selected: map[string]bool{}, testURL: "https://www.cloudflare.com/cdn-cgi/trace", ctx: context.Background()}
}
func (m ProxiesPage) Init() tea.Cmd { return nil }

func (m ProxiesPage) Filtered() []proxymanager.ProxyEntry {
	if m.Source == nil {
		return nil
	}
	q := strings.ToLower(strings.TrimSpace(m.Query))
	out := make([]proxymanager.ProxyEntry, 0)
	for _, e := range m.Source.List() {
		haystack := strings.ToLower(strings.Join([]string{e.ID, e.Host, string(e.Scheme), e.Label, e.Geo.Country, e.Geo.City, e.Geo.ASN}, " "))
		if q != "" && !strings.Contains(haystack, q) {
			continue
		}
		if m.StatusFilter == ProxyStatusAlive && !e.Alive {
			continue
		}
		if m.StatusFilter == ProxyStatusDead && e.Alive {
			continue
		}
		if m.TypeFilter != "" && e.Scheme != m.TypeFilter {
			continue
		}
		if m.CountryFilter != "" && !strings.EqualFold(e.Geo.Country, m.CountryFilter) {
			continue
		}
		if m.LatencyFilter > 0 && (e.Latency <= 0 || e.Latency > m.LatencyFilter) {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch m.Sort {
		case ProxySortLatency:
			if a.Latency == 0 {
				return false
			}
			if b.Latency == 0 {
				return true
			}
			return a.Latency < b.Latency
		case ProxySortType:
			return a.Scheme < b.Scheme
		case ProxySortCountry:
			return strings.ToLower(a.Geo.Country) < strings.ToLower(b.Geo.Country)
		default:
			return a.ID < b.ID
		}
	})
	return out
}

func (m *ProxiesPage) clampCursor() {
	if n := len(m.Filtered()); n == 0 {
		m.Cursor = 0
		m.Offset = 0
	} else if m.Cursor >= n {
		m.Cursor = n - 1
	}
	m.ensureVisible()
}

func (m ProxiesPage) visibleRows() int { return max(1, m.Height-9) }

// RowAt maps page-local terminal cells to an absolute filtered proxy row.
func (m ProxiesPage) RowAt(x, y int) (int, bool) {
	const firstRowY = 7
	entries := m.Filtered()
	row := m.Offset + y - firstRowY
	if x < 0 || y < firstRowY || y >= firstRowY+m.visibleRows() || row < 0 || row >= len(entries) {
		return 0, false
	}
	return row, true
}

func (m *ProxiesPage) ensureVisible() {
	n := len(m.Filtered())
	if n == 0 {
		m.Offset = 0
		return
	}
	rows := m.visibleRows()
	if m.Cursor < m.Offset {
		m.Offset = m.Cursor
	}
	if m.Cursor >= m.Offset+rows {
		m.Offset = m.Cursor - rows + 1
	}
	maxOffset := max(0, n-rows)
	if m.Offset > maxOffset {
		m.Offset = maxOffset
	}
	if m.Offset < 0 {
		m.Offset = 0
	}
}

func (m *ProxiesPage) moveCursor(delta int) {
	n := len(m.Filtered())
	if n == 0 {
		m.Cursor, m.Offset = 0, 0
		return
	}
	m.Cursor = min(n-1, max(0, m.Cursor+delta))
	m.ensureVisible()
}
func (m *ProxiesPage) copy(entries []proxymanager.ProxyEntry) {
	urls := make([]string, 0, len(entries))
	for _, e := range entries {
		urls = append(urls, e.ProxyURL())
	}
	if len(urls) == 0 {
		m.Message = "No proxy URLs to copy"
		return
	}
	if m.Clipboard == nil {
		m.Message = "Clipboard unavailable"
		return
	}
	if err := m.Clipboard.WriteText(strings.Join(urls, "\n")); err != nil {
		m.Message = "Copy failed: " + err.Error()
		return
	}
	m.Message = fmt.Sprintf("Copied %d proxy URL(s)", len(urls))
}
func cycleCountry(current string, entries []proxymanager.ProxyEntry) string {
	set := map[string]bool{}
	countries := []string{}
	for _, e := range entries {
		if e.Geo.Country != "" && !set[e.Geo.Country] {
			set[e.Geo.Country] = true
			countries = append(countries, e.Geo.Country)
		}
	}
	sort.Strings(countries)
	options := append([]string{""}, countries...)
	for i, v := range options {
		if strings.EqualFold(v, current) {
			return options[(i+1)%len(options)]
		}
	}
	return ""
}

func (m ProxiesPage) Update(msg tea.Msg) (ProxiesPage, tea.Cmd) {
	if m.Source == nil {
		return m, nil
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.Width = max(1, msg.Width)
		m.Height = max(1, msg.Height)
		m.clampCursor()
	case tea.MouseMsg:
		switch msg.Type {
		case tea.MouseWheelUp:
			m.moveCursor(-3)
		case tea.MouseWheelDown:
			m.moveCursor(3)
		case tea.MouseLeft:
			if row, ok := m.RowAt(msg.X, msg.Y); ok {
				m.Cursor = row
				id := m.Filtered()[row].ID
				if m.Selected[id] {
					delete(m.Selected, id)
				} else {
					m.Selected[id] = true
				}
			}
		}
	case testResultMsg:
		m.Message = fmt.Sprintf("Tested proxy %s: alive=%v latency=%s", msg.Result.ID, msg.Result.Alive, msg.Result.Latency.Round(time.Millisecond))
		m.testing = false
	case testAllResultMsg:
		m.testDone = len(msg.Results)
		m.testHealthy, m.testSlow, m.testDead = 0, 0, 0
		for _, result := range msg.Results {
			switch {
			case !result.Alive || result.Error != "":
				m.testDead++
			case result.Latency >= 2*time.Second:
				m.testSlow++
			default:
				m.testHealthy++
			}
		}
		m.testing = false
		if err := m.Source.Reload(m.ctx); err != nil {
			m.Message = fmt.Sprintf("Tested %d proxies; reload failed: %v", m.testDone, err)
		} else {
			m.Message = fmt.Sprintf("Tested %d proxies; runtime reloaded", m.testDone)
		}
	case importResultMsg:
		m.Message = fmt.Sprintf("Imported %d proxies", msg.Added)
		m.importing = false
	case tea.KeyMsg:
		key := msg.String()
		if m.importing {
			switch key {
			case "esc":
				m.importing = false
				m.importBuffer = ""
				m.Message = "Import cancelled"
			case "enter":
				path := strings.TrimSpace(m.importBuffer)
				m.importing = false
				m.importBuffer = ""
				if path == "" {
					m.Message = "Import cancelled: empty path"
					return m, nil
				}
				added, errs := m.Source.ImportFile(path)
				if len(errs) > 0 {
					m.Message = fmt.Sprintf("Imported %d proxies from %s (%d error(s): %s)", added, path, len(errs), strings.Join(errs, "; "))
				} else {
					m.Message = fmt.Sprintf("Imported %d proxies from %s", added, path)
				}
				m.clampCursor()
			case "backspace":
				if len(m.importBuffer) > 0 {
					m.importBuffer = m.importBuffer[:len(m.importBuffer)-1]
				}
			default:
				if msg.Type == tea.KeyRunes {
					m.importBuffer += string(msg.Runes)
				}
			}
			return m, nil
		}
		if m.Searching {
			switch key {
			case "esc":
				m.Searching = false
				m.searchBuffer = ""
			case "enter":
				m.Query = m.searchBuffer
				m.Searching = false
				m.Cursor = 0
			case "backspace":
				if len(m.searchBuffer) > 0 {
					m.searchBuffer = m.searchBuffer[:len(m.searchBuffer)-1]
				}
			default:
				if msg.Type == tea.KeyRunes {
					m.searchBuffer += string(msg.Runes)
				}
			}
			return m, nil
		}
		entries := m.Filtered()
		switch key {
		case "/":
			m.Searching = true
			m.searchBuffer = m.Query
		case "i":
			m.importing = true
			m.importBuffer = "data/proxies/"
			m.Message = "Type a proxy file path, then Enter to import (Esc to cancel)"
		case "up", "k":
			m.moveCursor(-1)
		case "down", "j":
			m.moveCursor(1)
		case "pgup":
			m.moveCursor(-m.visibleRows())
		case "pgdown":
			m.moveCursor(m.visibleRows())
		case "home":
			m.Cursor, m.Offset = 0, 0
		case "end":
			if len(entries) > 0 {
				m.Cursor = len(entries) - 1
				m.ensureVisible()
			}
		case " ":
			if len(entries) > 0 {
				id := entries[m.Cursor].ID
				if m.Selected[id] {
					delete(m.Selected, id)
				} else {
					m.Selected[id] = true
				}
			}
		case "esc":
			m.Query = ""
			m.StatusFilter = ProxyStatusAll
			m.TypeFilter = ""
			m.CountryFilter = ""
			m.LatencyFilter = 0
			m.Selected = map[string]bool{}
			m.Cursor = 0
			m.Message = "Filters and selection cleared"
		case "s":
			switch m.StatusFilter {
			case ProxyStatusAll:
				m.StatusFilter = ProxyStatusAlive
			case ProxyStatusAlive:
				m.StatusFilter = ProxyStatusDead
			default:
				m.StatusFilter = ProxyStatusAll
			}
			m.Cursor = 0
		case "y":
			switch m.TypeFilter {
			case "":
				m.TypeFilter = proxymanager.SchemeHTTP
			case proxymanager.SchemeHTTP:
				m.TypeFilter = proxymanager.SchemeHTTPS
			case proxymanager.SchemeHTTPS:
				m.TypeFilter = proxymanager.SchemeSOCKS5
			default:
				m.TypeFilter = ""
			}
			m.Cursor = 0
		case "c":
			m.CountryFilter = cycleCountry(m.CountryFilter, m.Source.List())
			m.Cursor = 0
		case "l":
			switch m.LatencyFilter {
			case 0:
				m.LatencyFilter = 50 * time.Millisecond
			case 50 * time.Millisecond:
				m.LatencyFilter = 100 * time.Millisecond
			case 100 * time.Millisecond:
				m.LatencyFilter = 250 * time.Millisecond
			case 250 * time.Millisecond:
				m.LatencyFilter = time.Second
			default:
				m.LatencyFilter = 0
			}
			m.Cursor = 0
		case "o":
			switch m.Sort {
			case ProxySortDefault:
				m.Sort = ProxySortLatency
			case ProxySortLatency:
				m.Sort = ProxySortType
			case ProxySortType:
				m.Sort = ProxySortCountry
			default:
				m.Sort = ProxySortDefault
			}
			m.Cursor = 0
		case "C":
			selected := []proxymanager.ProxyEntry{}
			for _, e := range m.Source.List() {
				if m.Selected[e.ID] {
					selected = append(selected, e)
				}
			}
			m.copy(selected)
		case "A":
			m.copy(entries)
		case "d", "delete":
			if len(entries) > 0 && m.Source.Remove(entries[m.Cursor].ID) {
				delete(m.Selected, entries[m.Cursor].ID)
				m.Message = "Proxy deleted"
			}
		case "t":
			if len(entries) > 0 && !m.testing {
				m.testing = true
				m.testTotal = len(m.Source.List())
				m.testDone, m.testHealthy, m.testSlow, m.testDead = 0, 0, 0, 0
				m.Message = "Testing proxies…"
				source, ctx, testURL := m.Source, m.ctx, m.testURL
				return m, func() tea.Msg {
					return testAllResultMsg{Results: source.TestAll(ctx, testURL)}
				}
			}
		case "g":
			if len(entries) > 0 {
				go m.Source.TestAll(m.ctx, m.testURL)
				m.Message = "Geo lookup triggered"
			}
		case "r":
			total, alive := m.Source.Count()
			m.Message = fmt.Sprintf("Proxies: %d total, %d alive", total, alive)
		}
		m.clampCursor()
	}
	return m, nil
}

func label(v string) string {
	if v == "" {
		return "all"
	}
	return v
}

// proxyKind classifies an entry into a ui.BadgeKind for its health pill.
func proxyKind(e proxymanager.ProxyEntry) ui.BadgeKind {
	switch {
	case !e.Alive:
		return ui.KindDead
	case e.Latency >= 2*time.Second:
		return ui.KindSlow
	default:
		return ui.KindHealthy
	}
}

// sortArrow returns a direction glyph for the active sort column.
func sortArrow(active bool) string {
	if active {
		return "▲"
	}
	return ""
}

func (m ProxiesPage) View() string {
	rows := []string{Title("Proxies", "search, filter, sort and copy proxy URLs"), ""}
	if m.Source == nil {
		return strings.Join(append(rows, "Proxy manager unavailable."), "\n")
	}
	stats := m.Source.Stats()
	rows = append(rows, fmt.Sprintf("Total: %d  Alive: %d  HTTP: %d  HTTPS: %d  SOCKS5: %d  Geo: %d", stats.Total, stats.Alive, stats.HTTP, stats.HTTPS, stats.SOCKS5, stats.GeoCount))
	if m.testing || m.testTotal > 0 {
		barW := 24
		bar := ui.ProgressBar(m.testDone, max(m.testTotal, 1), barW)
		summary := fmt.Sprintf("Test progress: %d/%d  Healthy: %d  Slow: %d  Dead: %d", m.testDone, m.testTotal, m.testHealthy, m.testSlow, m.testDead)
		rows = append(rows, "Testing "+bar+"  "+summary+fmt.Sprintf("  %s%d %s%d %s%d",
			ui.KindGlyph(ui.KindHealthy)+"H", m.testHealthy, ui.KindGlyph(ui.KindSlow)+"S", m.testSlow, ui.KindGlyph(ui.KindDead)+"D", m.testDead))
	}
	// Filter chips: [healthy][slow][dead][all] reflecting StatusFilter.
	chips := strings.Join([]string{
		ui.Chip("[healthy]", m.StatusFilter == ProxyStatusAlive, ui.KindHealthy),
		ui.Chip("[slow]", false, ui.KindSlow),
		ui.Chip("[dead]", m.StatusFilter == ProxyStatusDead, ui.KindDead),
		ui.Chip("[all]", m.StatusFilter == ProxyStatusAll, ui.KindOnline),
	}, "")
	query := m.Query
	if m.Searching {
		query = m.searchBuffer + "█"
	}
	latency := "all"
	if m.LatencyFilter > 0 {
		latency = "≤" + m.LatencyFilter.String()
	}
	rows = append(rows, chips)
	rows = append(rows, fmt.Sprintf("Search: %s  Status: %s  Type: %s  Country: %s  Latency: %s  Sort: %s %s", label(query), label(string(m.StatusFilter)), label(string(m.TypeFilter)), label(m.CountryFilter), latency, m.Sort, sortArrow(m.Sort != ProxySortDefault)), "")
	if m.importing {
		rows = append(rows, fmt.Sprintf("Import path: %s█", m.importBuffer), muted.Render("Enter to import, Esc to cancel"), "")
	}
	entries := m.Filtered()
	if len(entries) == 0 {
		rows = append(rows, "  No matching proxies.")
	} else {
		rows = append(rows, muted.Render(fmt.Sprintf("%-3s %-4s %-21s %-7s %-11s %-10s %-14s", "", "#", "HOST:PORT", "SCHEME", "HEALTH", "LATENCY", "COUNTRY/CITY")))
		start := min(m.Offset, len(entries)-1)
		end := min(len(entries), start+m.visibleRows())
		rows = append(rows, muted.Render(fmt.Sprintf("Showing %d-%d of %d", start+1, end, len(entries))))
		for i, e := range entries[start:end] {
			index := start + i
			pointer := "  "
			if index == m.Cursor {
				pointer = cursor.Render("› ")
			}
			checked := "[ ]"
			if m.Selected[e.ID] {
				checked = "[x]"
			}
			kind := proxyKind(e)
			badge := ui.BadgeGlyph(ui.KindGlyph(kind), string(kind), kind)
			latency := "—"
			if e.Latency > 0 {
				latency = e.Latency.Round(time.Millisecond).String()
			}
			geoText := label(e.Geo.Country)
			if e.Geo.City != "" {
				geoText += "/" + e.Geo.City
			}
			rows = append(rows, fmt.Sprintf("%s%-3s %-4s %-21s %-7s %-11s %-10s %-14s", pointer, checked, e.ID, fmt.Sprintf("%s:%d", e.Host, e.Port), e.Scheme, badge, latency, geoText))
		}
	}
	rows = append(rows, "", muted.Render("[/] search  [i] import  [s] status  [y] type  [c] country  [l] latency  [o] sort"), muted.Render("[space] select  [Esc] clear  [C] copy selected  [A] copy filtered  [↑/↓ PgUp/PgDn Home/End] move"))
	if m.Message != "" {
		rows = append(rows, "", m.Message)
	}
	if m.Status != "" {
		rows = append(rows, "", muted.Render(m.Status))
	}
	return strings.Join(rows, "\n")
}
