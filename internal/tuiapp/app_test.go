package tuiapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"nlwproxy/internal/metrics"
	"nlwproxy/internal/proxymanager"
	"nlwproxy/internal/tuiapp/pages"
)

type proxySource struct {
	entries []proxymanager.ProxyEntry
	rev     int
}

func newProxySource(count int) *proxySource {
	s := &proxySource{}
	for i := range count {
		s.entries = append(s.entries, proxymanager.ProxyEntry{ID: fmt.Sprintf("proxy-%03d", i), Host: "127.0.0.1", Port: 9000 + i, Scheme: proxymanager.SchemeHTTP})
	}
	return s
}
func (s *proxySource) bump() { s.rev++ }
func (s *proxySource) List() []proxymanager.ProxyEntry {
	return append([]proxymanager.ProxyEntry(nil), s.entries...)
}
func (s *proxySource) Count() (int, int) { return len(s.entries), 0 }
func (s *proxySource) Stats() proxymanager.Stats {
	stats := proxymanager.Stats{Total: len(s.entries)}
	for _, entry := range s.entries {
		if !entry.Alive {
			stats.Dead++
			continue
		}
		stats.Alive++
		if entry.Latency >= 2*time.Second {
			stats.Slow++
		} else {
			stats.Healthy++
		}
	}
	return stats
}
func (s *proxySource) ImportFile(string) (int, []string) { return 0, nil }
func (s *proxySource) TestSingle(context.Context, string, string) proxymanager.TestResult {
	return proxymanager.TestResult{}
}
func (s *proxySource) TestAll(context.Context, string) []proxymanager.TestResult { return nil }
func (s *proxySource) Get(id string) (proxymanager.ProxyEntry, bool) {
	for _, entry := range s.entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return proxymanager.ProxyEntry{}, false
}
func (s *proxySource) Remove(string) bool           { return false }
func (s *proxySource) Reload(context.Context) error { return nil }
func (s *proxySource) Snapshot() pages.OperationsSnapshot {
	return pages.OperationsSnapshot{Revision: uint64(s.rev)}
}

func updateModel(t *testing.T, model Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := model.Update(msg)
	result, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	return result, cmd
}

type fakeLifecycle struct {
	store    *Store
	started  chan struct{}
	stopped  chan struct{}
	startErr error
}

func (f *fakeLifecycle) Source() StateSource { return f.store }
func (f *fakeLifecycle) Start(context.Context) error {
	close(f.started)
	return f.startErr
}
func (f *fakeLifecycle) Stop(context.Context) error {
	close(f.stopped)
	return nil
}

func TestProxyOnlyDashboardAndLockedSettings(t *testing.T) {
	source := newProxySource(2)
	source.entries[0].Alive = true
	model := New(context.Background(), nil)
	model.operations = source
	model.proxiesPage = pages.NewProxiesPage(source)
	model.snapshot = Snapshot{Status: "ONLINE", ProxyOnly: true, HealthyProxies: 1, ActiveProxy: "127.0.0.1:9000", ProxyCountry: "United States"}
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 120, Height: 30})
	view := model.View()
	for _, want := range []string{"PROXY ONLY", "DIRECT BLOCKED", "1 HEALTHY", "Active routes", "United States"} {
		if !strings.Contains(view, want) {
			t.Fatalf("dashboard missing %q:\n%s", want, view)
		}
	}
	model.active, model.selected, model.focus = PageSettings, int(PageSettings), focusContent
	model.refreshSettings()
	settings := model.View()
	for _, want := range []string{"Proxy-only", "ON"} {
		if !strings.Contains(settings, want) {
			t.Fatalf("settings missing %q:\n%s", want, settings)
		}
	}
}

func TestProxyOnlyDashboardShowsNoHealthyError(t *testing.T) {
	model := New(context.Background(), nil)
	model.snapshot = Snapshot{Status: "ONLINE", ProxyOnly: true, HealthyProxies: 0}
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 100, Height: 24})
	if view := model.View(); !strings.Contains(view, "NO ACTIVE PROXY ROUTES") {
		t.Fatalf("missing no-route error:\n%s", view)
	}
}

func TestNavigationSelectsPage(t *testing.T) {
	model := New(context.Background(), nil)
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 100, Height: 30})
	model, _ = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	if model.selected != 1 || model.active != PageOverview {
		t.Fatalf("selection=%d active=%s", model.selected, model.active)
	}
	model, _ = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.active != PageModels {
		t.Fatalf("active=%s", model.active)
	}
	if got := model.View(); !strings.Contains(got, "Model traffic") {
		t.Fatalf("models placeholder missing from view:\n%s", got)
	}
}

func TestWindowResizeChangesResponsiveLayout(t *testing.T) {
	model := New(context.Background(), nil)
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 100, Height: 28})
	wide := model.View()
	if !strings.Contains(wide, "CONTROL PLANE") {
		t.Fatalf("wide sidebar missing:\n%s", wide)
	}
	if got := lipgloss.Width(wide); got != 100 {
		t.Fatalf("wide render width=%d", got)
	}

	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 54, Height: 18})
	narrow := model.View()
	if strings.Contains(narrow, "CONTROL PLANE") {
		t.Fatalf("sidebar should collapse in narrow view:\n%s", narrow)
	}
	if !strings.Contains(narrow, "NLWPROXY") {
		t.Fatalf("compact header missing:\n%s", narrow)
	}
	if got := lipgloss.Width(narrow); got != 54 {
		t.Fatalf("narrow render width=%d", got)
	}
}

func TestTallProxyPageCannotMoveSidebarOrStatusBar(t *testing.T) {
	source := newProxySource(1000)
	for i := range source.entries {
		source.entries[i].Alive = true
		source.entries[i].Latency = 100 * time.Millisecond
	}
	model := New(context.Background(), nil)
	model.operations = source
	model.proxiesPage = pages.NewProxiesPage(source)
	model.active, model.selected, model.focus = PageProxies, int(PageProxies), focusContent
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 120, Height: 30})
	view := model.View()
	if got := lipgloss.Height(view); got != 30 {
		t.Fatalf("render height=%d want=30", got)
	}
	lines := strings.Split(view, "\n")
	if len(lines) != 30 || !strings.Contains(lines[4], "Overview") || !strings.Contains(lines[len(lines)-1], "127.0.0.1") && !strings.Contains(lines[len(lines)-1], "gateway offline") {
		t.Fatalf("shell anchors moved:\n%s", view)
	}
}

func TestOverviewUsesLiveProxyHealthComposition(t *testing.T) {
	source := newProxySource(4)
	source.entries[0].Alive, source.entries[0].Latency = true, 100*time.Millisecond
	source.entries[1].Alive, source.entries[1].Latency = true, 3*time.Second
	model := New(context.Background(), nil)
	model.operations = source
	model.proxiesPage = pages.NewProxiesPage(source)
	model.snapshot = Snapshot{Status: "ONLINE", Connections: 2}
	model.refreshOverviewDash()
	data := model.overviewDash.Data
	if data.Total != 4 || data.Healthy != 1 || data.Slow != 1 || data.Dead != 2 || data.ActiveRoutes != 2 {
		t.Fatalf("overview health=%+v", data)
	}
}

func TestStableIdleViewAndNoIdleCommand(t *testing.T) {
	model := New(context.Background(), nil)
	if cmd := model.Init(); cmd != nil {
		t.Fatal("nil source must not schedule an idle command")
	}
	model, cmd := updateModel(t, model, tea.WindowSizeMsg{Width: 88, Height: 24})
	if cmd != nil {
		t.Fatal("resize must not schedule periodic redraw")
	}
	first := model.View()
	time.Sleep(10 * time.Millisecond)
	second := model.View()
	if first != second {
		t.Fatal("idle view changed without an event")
	}
	model, cmd = updateModel(t, model, struct{}{})
	if cmd != nil || model.View() != first {
		t.Fatal("unknown idle message changed or scheduled work")
	}
}

func TestUnfreezeImmediatelyCatchesUpToLatestSourceSnapshot(t *testing.T) {
	store := NewStore(nil, Snapshot{Gateway: "before", Requests: 1})
	model := New(context.Background(), store)
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 100, Height: 24})
	model, _ = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	store.Set(Snapshot{Gateway: "after", Requests: 9})
	model, _ = updateModel(t, model, ChangedMsg(store.Snapshot()))
	if model.snapshot.Gateway != "before" {
		t.Fatalf("frozen model consumed new snapshot: %+v", model.snapshot)
	}
	model, _ = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if model.snapshot.Gateway != "after" || model.snapshot.Requests != 9 {
		t.Fatalf("unfreeze did not catch up immediately: %+v", model.snapshot)
	}
}

func TestFreezePreservesRenderedProxySelectionUntilUnfrozen(t *testing.T) {
	source := newProxySource(250)
	model := New(context.Background(), nil)
	model.operations = source
	model.proxiesPage = pages.NewProxiesPage(source)
	model.active, model.selected, model.focus = PageProxies, int(PageProxies), focusContent
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 120, Height: 30})
	for range 180 {
		model, _ = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	}
	selected := source.List()[model.proxiesPage.Cursor].ID
	model, _ = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	frozen := model.View()
	if !strings.Contains(frozen, selected) || !strings.Contains(frozen, "FROZEN") {
		t.Fatalf("large-list selection or frozen indicator missing: selected=%q", selected)
	}
	source.bump()
	model, cmd := updateModel(t, model, ChangedMsg(Snapshot{Gateway: "changed"}))
	if cmd != nil || model.View() != frozen {
		t.Fatal("source update redrew the proxy page while selection was frozen")
	}
	model, _ = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if model.proxiesPage.Cursor != 180 || !strings.Contains(model.View(), selected) || strings.Contains(model.View(), "FROZEN") {
		t.Fatalf("unfreeze lost selection: cursor=%d selected=%q", model.proxiesPage.Cursor, selected)
	}
}

func TestProxyPageOwnsArrowKeysWithoutMovingPrimaryNavigation(t *testing.T) {
	source := newProxySource(3)
	model := New(context.Background(), nil)
	model.operations = source
	model.proxiesPage = pages.NewProxiesPage(source)
	model.active, model.selected, model.focus = PageProxies, int(PageProxies), focusContent
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 100, Height: 24})
	model, _ = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	if model.selected != int(PageProxies) || model.proxiesPage.Cursor != 1 {
		t.Fatalf("arrow key leaked to primary navigation: nav=%d proxy=%d", model.selected, model.proxiesPage.Cursor)
	}
}

func TestSidebarAndContentFocusNavigation(t *testing.T) {
	source := newProxySource(3)
	model := New(context.Background(), nil)
	model.operations = source
	model.proxiesPage = pages.NewProxiesPage(source)
	model.active, model.selected = PageProxies, int(PageProxies)
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 100, Height: 24})

	model, _ = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	if model.selected != int(PageRoutes) || model.proxiesPage.Cursor != 0 {
		t.Fatalf("sidebar arrow leaked to content: nav=%d proxy=%d", model.selected, model.proxiesPage.Cursor)
	}

	model, _ = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.active != PageRoutes || model.focus != focusContent {
		t.Fatalf("enter did not activate and focus content: active=%s focus=%v", model.active, model.focus)
	}
	model, _ = updateModel(t, model, tea.KeyMsg{Type: tea.KeyLeft})
	if model.focus != focusSidebar {
		t.Fatalf("left did not return to sidebar: focus=%v", model.focus)
	}

	model.selected = int(PageProxies)
	model, _ = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	if model.active != PageProxies || model.focus != focusContent {
		t.Fatalf("right did not enter selected menu: active=%s focus=%v", model.active, model.focus)
	}
	model, _ = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	if model.selected != int(PageProxies) || model.proxiesPage.Cursor != 1 {
		t.Fatalf("content arrow was not routed to page: nav=%d proxy=%d", model.selected, model.proxiesPage.Cursor)
	}
	model, _ = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEsc})
	if model.focus != focusSidebar {
		t.Fatalf("escape did not return to sidebar: focus=%v", model.focus)
	}
}

func TestTabTogglesFocusForEveryMenu(t *testing.T) {
	for index, page := range navigationPages {
		model := New(context.Background(), nil)
		model.selected = index
		model, _ = updateModel(t, model, tea.KeyMsg{Type: tea.KeyTab})
		if model.active != page || model.focus != focusContent {
			t.Fatalf("tab did not enter %s: active=%s focus=%v", page, model.active, model.focus)
		}
		model, _ = updateModel(t, model, tea.KeyMsg{Type: tea.KeyTab})
		if model.focus != focusSidebar {
			t.Fatalf("tab did not exit %s: focus=%v", page, model.focus)
		}
	}
}

func TestStatusBarDocumentsSelectionSafeCopy(t *testing.T) {
	model := New(context.Background(), nil)
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 120, Height: 24})
	view := model.View()
	for _, want := range []string{"f freeze", "Shift+drag select", "Ctrl+Shift+C copy"} {
		if !strings.Contains(view, want) {
			t.Fatalf("status bar missing %q:\n%s", want, view)
		}
	}
}

func TestMouseClickSidebarOpensPage(t *testing.T) {
	model := New(context.Background(), nil)
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 100, Height: 30})
	model, _ = updateModel(t, model, tea.MouseMsg{X: 3, Y: 6, Type: tea.MouseLeft})
	if model.selected != int(PageProxies) || model.active != PageProxies || model.focus != focusContent {
		t.Fatalf("selection=%d active=%s focus=%v", model.selected, model.active, model.focus)
	}
}

func TestMouseClickProxyRowUsesContentCoordinates(t *testing.T) {
	source := newProxySource(20)
	model := New(context.Background(), nil)
	model.operations = source
	model.proxiesPage = pages.NewProxiesPage(source)
	model.active, model.selected, model.focus = PageProxies, int(PageProxies), focusContent
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 100, Height: 24})
	model, _ = updateModel(t, model, tea.MouseMsg{X: model.contentOriginX() + 3, Y: 10, Type: tea.MouseLeft})
	if model.proxiesPage.Cursor != 2 || !model.proxiesPage.Selected["proxy-002"] {
		t.Fatalf("cursor=%d selected=%v", model.proxiesPage.Cursor, model.proxiesPage.Selected)
	}
}

func TestStoreBridgesMetricsEvents(t *testing.T) {
	events := metrics.NewEventBus(4)
	store := NewStore(events, Snapshot{Profile: "work", Gateway: "127.0.0.1:8787", Connections: 2})
	changes := store.Changes()
	events.Start()
	select {
	case <-changes:
	case <-time.After(time.Second):
		t.Fatal("metrics event did not wake state bridge")
	}
	got := store.Snapshot()
	if got.Profile != "work" || got.Active != 1 || got.Connections != 2 {
		t.Fatalf("snapshot=%+v", got)
	}
}

func TestLifecycleStartsBeforeOnlineAndQuitStops(t *testing.T) {
	store := NewStore(nil, Snapshot{Status: "STARTING"})
	runtime := &fakeLifecycle{store: store, started: make(chan struct{}), stopped: make(chan struct{})}
	model := NewRuntime(context.Background(), runtime)
	cmd := model.Init()
	if cmd == nil {
		t.Fatal("runtime model did not schedule startup")
	}
	msg := cmd()
	select {
	case <-runtime.started:
	default:
		t.Fatal("runtime was not started")
	}
	model, _ = updateModel(t, model, msg)
	if model.snapshot.Status != "ONLINE" {
		t.Fatalf("status = %q", model.snapshot.Status)
	}
	model, cmd = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("quit did not schedule graceful stop")
	}
	if _, ok := cmd().(runtimeStoppedMsg); !ok {
		t.Fatal("stop command returned unexpected message")
	}
	select {
	case <-runtime.stopped:
	default:
		t.Fatal("runtime was not stopped")
	}
}

func TestLifecycleStartupErrorIsDisplayed(t *testing.T) {
	want := errors.New("listen failed")
	runtime := &fakeLifecycle{store: NewStore(nil, Snapshot{}), started: make(chan struct{}), stopped: make(chan struct{}), startErr: want}
	model := NewRuntime(context.Background(), runtime)
	model, _ = updateModel(t, model, model.Init()())
	if model.snapshot.Status != "FAILED" || !strings.Contains(model.snapshot.Notice, want.Error()) {
		t.Fatalf("startup failure snapshot = %+v", model.snapshot)
	}
}
