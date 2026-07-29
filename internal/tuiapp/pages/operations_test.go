package pages

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"nlwproxy/internal/gateway"
	"nlwproxy/internal/metrics"
)

type operationsStub struct{ snapshot OperationsSnapshot }

func (s operationsStub) Snapshot() OperationsSnapshot { return s.snapshot }

func fixtureOperations() OperationsSnapshot {
	return OperationsSnapshot{
		Status: "online", RequestsPerMinute: 12.5, TokensPerMinute: 4200,
		Global: OperationStats{Total: 25, Errors: 2, Active: 3, InputTokens: 1200, OutputTokens: 800, TTFTP50: 80 * time.Millisecond, TTFTP95: 210 * time.Millisecond, DurationP50: 450 * time.Millisecond, DurationP95: 1300 * time.Millisecond},
		Models: map[string]OperationStats{"gpt-5": {Total: 15, Errors: 1, InputTokens: 700, OutputTokens: 500, TTFTP50: 70 * time.Millisecond, TTFTP95: 180 * time.Millisecond, DurationP50: 400 * time.Millisecond, DurationP95: time.Second}},
		Routes: map[string]OperationStats{"direct": {Total: 20, Errors: 1, Active: 2, InputTokens: 1000, OutputTokens: 600, Latency: 95 * time.Millisecond}},
		Recent: []metrics.Request{
			{RequestID: "req-ok", RouteID: "direct", RequestedModel: "gpt-5", Endpoint: "/v1/responses", Status: 200, StartedAt: time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC), Duration: 400 * time.Millisecond, TTFT: 70 * time.Millisecond, TotalTokens: 50},
			{RequestID: "req-bad", RouteID: "proxy", RequestedModel: "claude", Endpoint: "/v1/chat/completions", Status: 503, StartedAt: time.Date(2026, 7, 27, 12, 1, 0, 0, time.UTC), Duration: 2 * time.Second, TTFT: 300 * time.Millisecond, TotalTokens: 20, ErrorCode: "upstream"},
		},
		RequestSparkline: []float64{2, 4, 3, 8}, LatencySparkline: []float64{80, 120, 90, 210},
	}
}

func TestOperationsOverviewResponsiveBreakpoints(t *testing.T) {
	for _, tc := range []struct {
		width        int
		want, absent string
	}{{140, "TRAFFIC  &  LATENCY", ""}, {100, "TRAFFIC", "TRAFFIC  &  LATENCY"}, {58, "RPM", "P95 LATENCY"}} {
		m := NewOperationsOverview(operationsStub{fixtureOperations()})
		m, _ = m.Update(tea.WindowSizeMsg{Width: tc.width, Height: 30})
		view := m.View()
		if !strings.Contains(view, tc.want) || (tc.absent != "" && strings.Contains(view, tc.absent)) {
			t.Fatalf("width %d layout:\n%s", tc.width, view)
		}
		if got := lipgloss.Width(view); got > tc.width {
			t.Fatalf("width %d rendered %d", tc.width, got)
		}
	}
}

func TestModelsDetailAndTokenBar(t *testing.T) {
	m := NewModelsWithOperations([]gateway.Model{{ID: "gpt-5", Name: "GPT 5", OwnedBy: "openai"}}, "gpt-5", operationsStub{fixtureOperations()})
	m.SetSize(110, 28)
	view := m.View()
	for _, want := range []string{"MODEL DETAIL", "gpt-5", "openai", "P50", "P95", "INPUT", "OUTPUT", "█"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q:\n%s", want, view)
		}
	}
}

func TestRequestsFiltersAndSorts(t *testing.T) {
	m := NewOperationsRequests(operationsStub{fixtureOperations()})
	m.SetSize(120, 30)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if view := m.View(); !strings.Contains(view, "503") || strings.Contains(view, "200") {
		t.Fatalf("error filter:\n%s", view)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if !strings.Contains(m.View(), "SORT duration") {
		t.Fatalf("sort indicator missing:\n%s", m.View())
	}
}

func TestRequestsAndLogsRenderProxyGeo(t *testing.T) {
	data := fixtureOperations()
	data.Recent[0].ProxyID = "proxy-7"
	data.Recent[0].ProxyCountry = "Singapore"
	data.Recent[0].ProxyCity = "Singapore"
	source := operationsStub{data}
	requests := NewOperationsRequests(source)
	requests.SetSize(140, 30)
	for _, want := range []string{"PROXY", "GEO", "proxy-7", "Singapore"} {
		if view := requests.View(); !strings.Contains(view, want) {
			t.Fatalf("requests missing %q:\n%s", want, view)
		}
	}
	logs := NewLogs(source)
	logs.SetSize(140, 30)
	for _, want := range []string{"PROXY", "GEO", "proxy-7", "Singapore"} {
		if view := logs.View(); !strings.Contains(view, want) {
			t.Fatalf("logs missing %q:\n%s", want, view)
		}
	}
}

func TestLogsRenderSeverityAndMetadata(t *testing.T) {
	m := NewLogs(operationsStub{fixtureOperations()})
	m.SetSize(90, 20)
	view := m.View()
	for _, want := range []string{"Logs", "INFO", "ERROR", "upstream"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q:\n%s", want, view)
		}
	}
}
