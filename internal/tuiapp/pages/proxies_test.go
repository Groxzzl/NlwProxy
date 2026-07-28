package pages

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"nlwproxy/internal/geo"
	"nlwproxy/internal/proxymanager"
	"nlwproxy/internal/tuiapp/clipboard"
)

type proxySourceStub struct {
	entries     []proxymanager.ProxyEntry
	testResults []proxymanager.TestResult
	reloads     int
}

func proxyKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func (s *proxySourceStub) List() []proxymanager.ProxyEntry {
	return append([]proxymanager.ProxyEntry(nil), s.entries...)
}
func (s *proxySourceStub) Count() (int, int) { return len(s.entries), 0 }
func (s *proxySourceStub) Stats() proxymanager.Stats {
	return proxymanager.Stats{Total: len(s.entries)}
}
func (s *proxySourceStub) ImportFile(string) (int, []string) { return 0, nil }
func (s *proxySourceStub) TestSingle(context.Context, string, string) proxymanager.TestResult {
	return proxymanager.TestResult{}
}
func (s *proxySourceStub) TestAll(context.Context, string) []proxymanager.TestResult {
	return append([]proxymanager.TestResult(nil), s.testResults...)
}
func (s *proxySourceStub) Get(id string) (proxymanager.ProxyEntry, bool) {
	for _, e := range s.entries {
		if e.ID == id {
			return e, true
		}
	}
	return proxymanager.ProxyEntry{}, false
}
func (s *proxySourceStub) Remove(id string) bool        { return false }
func (s *proxySourceStub) Reload(context.Context) error { s.reloads++; return nil }

func proxyEntries() []proxymanager.ProxyEntry {
	return []proxymanager.ProxyEntry{
		{ID: "p1", Host: "10.0.0.1", Port: 80, Scheme: proxymanager.SchemeHTTP, Alive: true, Latency: 90 * time.Millisecond, Geo: geo.Result{Country: "US", City: "NY"}},
		{ID: "p2", Host: "example.de", Port: 1080, Scheme: proxymanager.SchemeSOCKS5, Alive: false, Latency: 200 * time.Millisecond, Geo: geo.Result{Country: "DE"}},
		{ID: "p3", Host: "10.0.0.3", Port: 443, Scheme: proxymanager.SchemeHTTPS, Alive: true, Latency: 20 * time.Millisecond, Geo: geo.Result{Country: "US"}},
	}
}

func TestProxiesSearchFilterSort(t *testing.T) {
	m := NewProxiesPage(&proxySourceStub{entries: proxyEntries()})
	m.Query = "10.0"
	m.StatusFilter = ProxyStatusAlive
	m.CountryFilter = "US"
	m.LatencyFilter = 50 * time.Millisecond
	got := m.Filtered()
	if len(got) != 1 || got[0].ID != "p3" {
		t.Fatalf("filtered=%v", got)
	}
	m.Query, m.StatusFilter, m.CountryFilter, m.LatencyFilter = "", ProxyStatusAll, "", 0
	m.Sort = ProxySortLatency
	got = m.Filtered()
	if got[0].ID != "p3" || got[2].ID != "p2" {
		t.Fatalf("latency order=%v", got)
	}
	m.Sort = ProxySortType
	got = m.Filtered()
	if got[0].Scheme != proxymanager.SchemeHTTP || got[2].Scheme != proxymanager.SchemeSOCKS5 {
		t.Fatalf("type order=%v", got)
	}
}

func TestProxiesSearchModeAndFilterCycles(t *testing.T) {
	m := NewProxiesPage(&proxySourceStub{entries: proxyEntries()})
	m, _ = m.Update(proxyKey('/'))
	if !m.Searching {
		t.Fatal("search not active")
	}
	m, _ = m.Update(proxyKey('d'))
	m, _ = m.Update(proxyKey('e'))
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.Query != "de" || m.Searching {
		t.Fatalf("query=%q searching=%v", m.Query, m.Searching)
	}
	m, _ = m.Update(proxyKey('s'))
	m, _ = m.Update(proxyKey('y'))
	m, _ = m.Update(proxyKey('c'))
	m, _ = m.Update(proxyKey('l'))
	m, _ = m.Update(proxyKey('o'))
	if m.StatusFilter == ProxyStatusAll || m.TypeFilter == "" || m.CountryFilter == "" || m.LatencyFilter == 0 || m.Sort == ProxySortDefault {
		t.Fatalf("filters did not cycle: %+v", m)
	}
}

func TestProxiesSelectClearAndCopyURLs(t *testing.T) {
	var copied string
	m := NewProxiesPage(&proxySourceStub{entries: proxyEntries()})
	m.Clipboard = clipboard.WriterFunc(func(s string) error { copied = s; return nil })
	m, _ = m.Update(proxyKey(' '))
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(proxyKey(' '))
	m, _ = m.Update(proxyKey('C'))
	if copied != "http://10.0.0.1:80\nsocks5://example.de:1080" {
		t.Fatalf("copied=%q", copied)
	}
	m.Query = "10.0"
	m, _ = m.Update(proxyKey('A'))
	if copied != "http://10.0.0.1:80\nhttps://10.0.0.3:443" {
		t.Fatalf("filtered copy=%q", copied)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.Query != "" || len(m.Selected) != 0 {
		t.Fatalf("escape did not clear: %+v", m)
	}
}

func TestProxiesCopyFailureAndLegend(t *testing.T) {
	m := NewProxiesPage(&proxySourceStub{entries: proxyEntries()})
	m.Clipboard = clipboard.WriterFunc(func(string) error { return errors.New("denied") })
	m, _ = m.Update(proxyKey('A'))
	if !strings.Contains(m.Message, "denied") {
		t.Fatalf("message=%q", m.Message)
	}
	view := m.View()
	for _, want := range []string{"[/] search", "[space] select", "[C] copy selected", "[A] copy filtered", "Sort:"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q:\n%s", want, view)
		}
	}
}

func TestProxiesTestAllShowsProgressSummary(t *testing.T) {
	source := &proxySourceStub{entries: proxyEntries(), testResults: []proxymanager.TestResult{
		{ID: "p1", Alive: true, Latency: 90 * time.Millisecond},
		{ID: "p2", Alive: true, Latency: 3 * time.Second},
		{ID: "p3", Error: "connection refused"},
	}}
	m := NewProxiesPage(source)
	m, cmd := m.Update(proxyKey('t'))
	if cmd == nil || !m.testing {
		t.Fatalf("test did not start asynchronously: testing=%v cmd=%v", m.testing, cmd)
	}
	if view := m.View(); !strings.Contains(view, "Test progress: 0/3  Healthy: 0  Slow: 0  Dead: 0") {
		t.Fatalf("missing initial progress:\n%s", view)
	}
	m, _ = m.Update(cmd())
	if source.reloads != 1 {
		t.Fatalf("completed test reloads=%d, want 1", source.reloads)
	}
	view := m.View()
	if !strings.Contains(view, "Test progress: 3/3  Healthy: 1  Slow: 1  Dead: 1") {
		t.Fatalf("missing completed summary:\n%s", view)
	}
	if m.testing {
		t.Fatal("testing flag remained set")
	}
}

func largeProxySource(n int) *proxySourceStub {
	entries := make([]proxymanager.ProxyEntry, n)
	for i := range entries {
		entries[i] = proxymanager.ProxyEntry{ID: fmt.Sprintf("proxy-%05d", i+1), Host: fmt.Sprintf("10.0.%d.%d", (i>>8)&255, i&255), Port: 8000 + i, Scheme: proxymanager.SchemeHTTP}
	}
	return &proxySourceStub{entries: entries}
}

func TestProxiesPageVirtualizesTenThousandRows(t *testing.T) {
	m := NewProxiesPage(largeProxySource(10_000))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 14})
	view := m.View()
	if !strings.Contains(view, "Showing 1-5 of 10000") {
		t.Fatalf("missing visible range:\n%s", view)
	}
	if !strings.Contains(view, "proxy-00001") || !strings.Contains(view, "proxy-00005") {
		t.Fatalf("visible rows missing:\n%s", view)
	}
	if strings.Contains(view, "proxy-00006") || strings.Contains(view, "proxy-10000") {
		t.Fatalf("off-screen row rendered:\n%s", view)
	}
}

func TestProxiesPageNavigationKeepsCursorVisible(t *testing.T) {
	m := NewProxiesPage(largeProxySource(10_000))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 14})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if m.Cursor != 5 || m.Offset != 1 {
		t.Fatalf("after page down cursor=%d offset=%d", m.Cursor, m.Offset)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if m.Cursor != 9_999 || m.Offset != 9_995 {
		t.Fatalf("after end cursor=%d offset=%d", m.Cursor, m.Offset)
	}
	if got := m.View(); !strings.Contains(got, "Showing 9996-10000 of 10000") || !strings.Contains(got, "proxy-10000") {
		t.Fatalf("end range incorrect:\n%s", got)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyHome})
	if m.Cursor != 0 || m.Offset != 0 {
		t.Fatalf("after home cursor=%d offset=%d", m.Cursor, m.Offset)
	}
}

func TestProxiesPageMouseWheelAndResize(t *testing.T) {
	m := NewProxiesPage(largeProxySource(10_000))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 14})
	m, _ = m.Update(tea.MouseMsg{Type: tea.MouseWheelDown})
	if m.Cursor != 3 || m.Offset != 0 {
		t.Fatalf("wheel down cursor=%d offset=%d", m.Cursor, m.Offset)
	}
	m, _ = m.Update(tea.MouseMsg{Type: tea.MouseWheelDown})
	if m.Cursor != 6 || m.Offset != 2 {
		t.Fatalf("second wheel cursor=%d offset=%d", m.Cursor, m.Offset)
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 12})
	if m.Cursor != 6 || m.Offset != 4 {
		t.Fatalf("resize cursor=%d offset=%d", m.Cursor, m.Offset)
	}
	m, _ = m.Update(tea.MouseMsg{Type: tea.MouseWheelUp})
	if m.Cursor != 3 || m.Offset != 3 {
		t.Fatalf("wheel up cursor=%d offset=%d", m.Cursor, m.Offset)
	}
}

func TestProxiesPageMouseRowMappingAndSelection(t *testing.T) {
	m := NewProxiesPage(largeProxySource(20))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 14})
	if row, ok := m.RowAt(0, 9); !ok || row != 2 {
		t.Fatalf("row=%d ok=%v", row, ok)
	}
	m, _ = m.Update(tea.MouseMsg{X: 4, Y: 9, Type: tea.MouseLeft})
	if m.Cursor != 2 || !m.Selected["proxy-00003"] {
		t.Fatalf("cursor=%d selected=%v", m.Cursor, m.Selected)
	}
	m, _ = m.Update(tea.MouseMsg{X: 4, Y: 9, Type: tea.MouseLeft})
	if m.Selected["proxy-00003"] {
		t.Fatal("second row click did not toggle selection off")
	}
	if _, ok := m.RowAt(0, 6); ok {
		t.Fatal("header mapped to a proxy row")
	}
}

// importSourceStub records ImportFile invocations and returns a scripted result.
type importSourceStub struct {
	proxySourceStub
	calledWith []string
	added      int
	errs       []string
}

func (s *importSourceStub) ImportFile(path string) (int, []string) {
	s.calledWith = append(s.calledWith, path)
	return s.added, s.errs
}

func TestProxiesImportModeShowsPrompt(t *testing.T) {
	m := NewProxiesPage(&proxySourceStub{entries: proxyEntries()})
	m, _ = m.Update(proxyKey('i'))
	if !m.importing {
		t.Fatal("import mode not active after pressing i")
	}
	view := m.View()
	if !strings.Contains(view, "Import path: data/proxies/") {
		t.Fatalf("prompt missing default hint:\n%s", view)
	}
	if !strings.Contains(view, "Enter to import, Esc to cancel") {
		t.Fatalf("prompt missing instructions:\n%s", view)
	}
	// Typing extends the buffer and is reflected with a cursor glyph.
	for _, r := range "webshare-02.txt" {
		m, _ = m.Update(proxyKey(r))
	}
	if !strings.Contains(m.View(), "Import path: data/proxies/webshare-02.txt█") {
		t.Fatalf("typed path not rendered with cursor:\n%s", m.View())
	}
	// Esc cancels without importing.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.importing {
		t.Fatal("Esc did not cancel import mode")
	}
	if strings.Contains(m.View(), "Import path:") {
		t.Fatalf("prompt still shown after cancel:\n%s", m.View())
	}
}

func TestProxiesImportInvokesImportFileOnEnter(t *testing.T) {
	source := &importSourceStub{added: 4}
	source.entries = proxyEntries()
	m := NewProxiesPage(source)
	m, _ = m.Update(proxyKey('i'))
	for _, r := range "webshare-02.txt" {
		m, _ = m.Update(proxyKey(r))
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(source.calledWith) != 1 || source.calledWith[0] != "data/proxies/webshare-02.txt" {
		t.Fatalf("ImportFile calls=%v", source.calledWith)
	}
	if m.importing {
		t.Fatal("import mode still active after Enter")
	}
	if !strings.Contains(m.Message, "Imported 4 proxies") {
		t.Fatalf("result message=%q", m.Message)
	}

	// Errors from ImportFile are surfaced in the message.
	source.added, source.errs = 1, []string{"line 3 malformed"}
	m, _ = m.Update(proxyKey('i'))
	for _, r := range "bad.txt" {
		m, _ = m.Update(proxyKey(r))
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.Message, "line 3 malformed") {
		t.Fatalf("error not surfaced: %q", m.Message)
	}
}
