package tuiapp

import (
	"context"
	"strings"
	"testing"
	"time"

	"nlwproxy/internal/config"
	"nlwproxy/internal/metrics"
	"nlwproxy/internal/profiles"
	gatewayruntime "nlwproxy/internal/runtime"
)

type integrationCredentials map[string]string

func (c integrationCredentials) Lookup(name string) (string, error) { return c[name], nil }

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
