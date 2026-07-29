package tuiapp

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"nlwproxy/internal/config"
	"nlwproxy/internal/metrics"
	"nlwproxy/internal/profiles"
	gatewayruntime "nlwproxy/internal/runtime"
	"nlwproxy/internal/tuiapp/pages"
)

type integrationCredentials map[string]string

func (c integrationCredentials) Lookup(name string) (string, error) { return c[name], nil }

func TestRuntimeEventsAutoRefreshRequestsWithoutKeypress(t *testing.T) {
	cfg := config.Default()
	cfg.Server.Listen = "127.0.0.1:0"
	cfg.Server.LocalTokenEnv = "AUTO_LOCAL_TOKEN"
	cfg.Upstreams = []config.Upstream{{Name: "primary", BaseURL: "https://example.test/v1", APIKeyEnv: "AUTO_UP_KEY", Enabled: true}}
	runtime, err := gatewayruntime.New(gatewayruntime.Options{Profile: profiles.Profile{ID: "auto", Name: "Auto", Config: cfg}, Credentials: integrationCredentials{"AUTO_LOCAL_TOKEN": "local", "AUTO_UP_KEY": "up"}})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewRuntimeAdapter(runtime)
	model := New(context.Background(), adapter.Source())
	model.operations = adapter.Operations()
	model.requestsPage = pages.NewOperationsRequests(model.operations)
	model.active, model.selected, model.focus = PageRequests, int(PageRequests), focusContent
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 140, Height: 30})

	wait := Notify(context.Background(), adapter.Source())
	started := metrics.Request{RequestID: "req-live", RequestedModel: "deepseek-v4", RouteID: "primary", Endpoint: "/v1/chat/completions", StartedAt: time.Now()}
	runtime.Events().Start(started)
	model, _ = updateModel(t, model, wait())
	if view := model.View(); !strings.Contains(view, "deepseek-v4") {
		t.Fatalf("active request did not auto-refresh:\n%s", view)
	}

	wait = Notify(context.Background(), adapter.Source())
	started.State, started.TTFT = metrics.RequestStreaming, 250*time.Millisecond
	runtime.Events().Update(started)
	model, _ = updateModel(t, model, wait())
	if view := model.View(); !strings.Contains(view, "250ms") {
		t.Fatalf("streaming TTFT did not auto-refresh:\n%s", view)
	}

	wait = Notify(context.Background(), adapter.Source())
	started.Status, started.InputTokens, started.OutputTokens = 200, 12, 3
	started.TotalTokens, started.Duration = 15, 900*time.Millisecond
	runtime.Events().Publish(started)
	model, _ = updateModel(t, model, wait())
	view := model.View()
	for _, want := range []string{"15", "900ms", "200"} {
		if !strings.Contains(view, want) {
			t.Fatalf("completed request missing %q after auto-refresh:\n%s", want, view)
		}
	}
}

func TestRuntimeEventRendersOperationsDashboard(t *testing.T) {
	cfg := config.Default()
	cfg.Server.Listen = "127.0.0.1:0"
	cfg.Server.LocalTokenEnv = "LOCAL_TOKEN"
	cfg.Upstreams = []config.Upstream{{Name: "primary", BaseURL: "https://example.test/v1", APIKeyEnv: "UP_KEY", Priority: 1, Weight: 1, Enabled: true}}
	runtime, err := gatewayruntime.New(gatewayruntime.Options{Profile: profiles.Profile{ID: "test", Name: "Test", Config: cfg}, Credentials: integrationCredentials{"LOCAL_TOKEN": "local", "UP_KEY": "up"}})
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewRuntimeAdapter(runtime)
	event := metrics.Request{RequestID: "req-1", RequestedModel: "deepseek-v4", RouteID: "primary", Endpoint: "/v1/chat/completions", StartedAt: time.Now(), State: metrics.RequestCompleted, Status: 200, InputTokens: 100, OutputTokens: 25, TotalTokens: 125, Duration: 2 * time.Second, TTFT: 300 * time.Millisecond}
	runtime.Events().Start(event)
	runtime.Events().Publish(event)
	model := NewRuntime(context.Background(), adapter)
	view := model.DebugView(140, 40)
	for _, want := range []string{"QUICK CONNECT", "TRAFFIC", "HEALTH"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}
