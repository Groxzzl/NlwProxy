package console

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"nlwproxy/internal/metrics"
)

func TestRenderDashboardV2ShowsOperationalMetadataAndFullKey(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	view := DashboardView{
		Status: "ONLINE", Started: now.Add(-90 * time.Second), BaseURL: "http://127.0.0.1:8787/v1",
		APIKey: "gateway-secret", ShowAPIKey: true, Provider: "Acme", ModelAlias: "opencode-route",
		InputTokens: 1200, OutputTokens: 345,
		Models: []ModelStat{{Name: "gpt-test", Requests: 3, InputTokens: 1200, OutputTokens: 345, Errors: 1}},
		Routes: []RouteStat{{Name: "primary", Transport: "direct", State: "healthy", Requests: 3, Active: 1, Latency: 25 * time.Millisecond}},
		Recent: []metrics.Request{{RequestID: "42", RouteID: "primary", Endpoint: "/v1/chat/completions", RequestedModel: "gpt-test", Status: 200, StartedAt: now, Duration: 40 * time.Millisecond}},
		Now:    now,
	}
	got := RenderDashboardV2(view, false, 110)
	for _, want := range []string{"NLWPROXY CONSOLE V2", "ONLINE", "1m30s", "gateway-secret", "Acme", "TOKEN TOTALS", "1,200", "345", "gpt-test", "primary", "direct", "RECENT REQUESTS", "/v1/chat/completions", "[R] Refresh", "[Q] Quit"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "prompt") && strings.Contains(got, "private") {
		t.Fatal("content leaked")
	}
}

func TestRenderDashboardV2CanHideGatewayKey(t *testing.T) {
	got := RenderDashboardV2(DashboardView{APIKey: "gateway-secret", ShowAPIKey: false}, false, 90)
	if strings.Contains(got, "gateway-secret") || !strings.Contains(got, MaskSecret("gateway-secret")) {
		t.Fatal(got)
	}
}

func TestAggregateMetadataBuildsPerModelAndRouteTotals(t *testing.T) {
	events := []metrics.Request{
		{RequestedModel: "model-a", RouteID: "route-a", Status: 200, RequestBytes: 100, ResponseBytes: 30},
		{RequestedModel: "model-a", RouteID: "route-a", Status: 500, RequestBytes: 20, ResponseBytes: 5},
	}
	models, routes, in, out := AggregateMetadata(events, nil)
	if len(models) != 1 || models[0].Requests != 2 || models[0].Errors != 1 || in != 120 || out != 35 {
		t.Fatalf("models=%+v in=%d out=%d", models, in, out)
	}
	if len(routes) != 1 || routes[0].Name != "route-a" || routes[0].Requests != 2 {
		t.Fatalf("routes=%+v", routes)
	}
}

func TestControllerActionsDispatchAndQuitCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var called []Action
	ctl := Controller{Cancel: cancel, Handle: func(_ context.Context, a Action) error { called = append(called, a); return nil }}
	for _, key := range []byte("RTSCBAMPLH") {
		if quit, err := ctl.Dispatch(ctx, key); quit || err != nil {
			t.Fatalf("key %q quit=%v err=%v", key, quit, err)
		}
	}
	if quit, err := ctl.Dispatch(ctx, 'q'); !quit || err != nil {
		t.Fatalf("quit=%v err=%v", quit, err)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("Q did not cancel")
	}
	if len(called) != 10 {
		t.Fatalf("called=%v", called)
	}
}

func TestRunEventLoopRefreshesAndHandlesRedirectedInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out bytes.Buffer
	draws := 0
	ctl := Controller{Cancel: cancel, Handle: func(context.Context, Action) error { return nil }}
	err := RunEventLoop(ctx, strings.NewReader("rq"), &out, 5*time.Millisecond, ctl, func() string { draws++; return "frame\n" })
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if draws < 2 || !strings.Contains(out.String(), "frame") {
		t.Fatalf("draws=%d out=%q", draws, out.String())
	}
}
