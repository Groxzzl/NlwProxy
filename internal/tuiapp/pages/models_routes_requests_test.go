package pages

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"nlwproxy/internal/gateway"
	"nlwproxy/internal/metrics"
	"nlwproxy/internal/routing"
)

type routeStub map[string]routing.RouteSnapshot

func (s routeStub) Snapshots(time.Time) map[string]routing.RouteSnapshot { return s }

type requestStub struct{ snapshot metrics.Snapshot }

func (s requestStub) Snapshot() metrics.Snapshot { return s.snapshot }

func TestModelsSearchAndSelection(t *testing.T) {
	m := NewModels([]gateway.Model{{ID: "alpha/model", Name: "Alpha"}, {ID: "beta/model", Name: "Beta"}}, "")
	m.Filter.SetValue("beta")
	if got := m.Filtered(); len(got) != 1 || got[0].ID != "beta/model" {
		t.Fatalf("filtered=%+v", got)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.Selected != "beta/model" {
		t.Fatalf("selected=%q", m.Selected)
	}
	view := m.View()
	for _, want := range []string{"Models", "beta", "beta/model", "selected"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q:\n%s", want, view)
		}
	}
}

func TestRoutesAndRequestsRenderAPISnapshots(t *testing.T) {
	cfg := testProfile().Config
	cfg.Routing.Strategy = "failover"
	routes := NewRoutes(cfg, routeStub{"OpenAI": {Health: routing.Healthy, Circuit: routing.CircuitClosed, Transport: "direct", Active: 1, Total: 4, Errors: 1, Latency: 12 * time.Millisecond, InputTokens: 10, OutputTokens: 5, ExitIP: routing.ExitIP{IP: "203.0.113.7"}}})
	rv := routes.View()
	for _, want := range []string{"Routes", "failover", "OpenAI", "healthy/closed", "12ms", "203.0.113.7"} {
		if !strings.Contains(rv, want) {
			t.Fatalf("route missing %q:\n%s", want, rv)
		}
	}
	events := metrics.Snapshot{Total: 1, Errors: 1, Events: []metrics.Request{{RequestID: "req-1", Endpoint: "/v1/responses", RequestedModel: "alpha/model", RouteID: "OpenAI", Status: 503, Duration: 20 * time.Millisecond, TTFT: 5 * time.Millisecond, TotalTokens: 15, RetryCount: 1, ErrorCode: "upstream"}}}
	q := NewRequests(requestStub{snapshot: events})
	view := q.View()
	for _, want := range []string{"Requests", "metadata only", "req-1", "alpha/model", "503", "20ms", "upstream"} {
		if !strings.Contains(view, want) {
			t.Fatalf("request missing %q:\n%s", want, view)
		}
	}
}
