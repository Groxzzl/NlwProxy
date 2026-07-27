package console

import (
	"bytes"
	"context"
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
	for _, want := range []string{"NLWPROXY PREMIUM", "ONLINE", "1m30s", "gateway-secret", "Acme", "TOKEN TOTALS", "1,200", "345", "gpt-test", "primary", "direct", "RECENT REQUESTS", "/v1/chat/completions", "[R] Refresh", "[Q] Quit"} {
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
		{RequestedModel: "model-a", RouteID: "route-a", Status: 200, InputTokens: 100, OutputTokens: 30},
		{RequestedModel: "model-a", RouteID: "route-a", Status: 500, InputTokens: 20, OutputTokens: 5},
	}
	models, routes, in, out := AggregateMetadata(events, nil)
	if len(models) != 1 || models[0].Requests != 2 || models[0].Errors != 1 || in != 120 || out != 35 {
		t.Fatalf("models=%+v in=%d out=%d", models, in, out)
	}
	if len(routes) != 1 || routes[0].Name != "route-a" || routes[0].Requests != 2 {
		t.Fatalf("routes=%+v", routes)
	}
}

func TestDashboardShowsProfileExitIPLoadAndPremiumActions(t *testing.T) {
	got := RenderDashboardV2(DashboardView{Profile: "Work", Routes: []RouteStat{{Name: "primary", ExitIP: "203.0.113.9", Load: 25}}}, false, 110)
	for _, want := range []string{"PROFILE", "Work", "EXIT IP", "203.0.113.9", "25%", "[N] New", "[E] Edit", "[W] Switch", "[D] Delete"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestControllerActionsDispatchAndQuitCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var called []Action
	ctl := Controller{Cancel: cancel, Handle: func(_ context.Context, a Action) error { called = append(called, a); return nil }}
	for _, key := range []byte("RCNEWPDMLH") {
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

func TestEventLoopInteractiveUsesAlternateScreenAndRestoresState(t *testing.T) {
	var out bytes.Buffer
	err := runEventLoop(context.Background(), strings.NewReader("q"), &out, time.Hour, Controller{}, func() string { return "FRAME\n" }, TerminalCapabilities{Interactive: true, Color: true, Width: 100})
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.HasPrefix(got, EnterScreen(true)) || !strings.HasSuffix(got, LeaveScreen(true)) {
		t.Fatalf("terminal lifecycle not restored: %q", got)
	}
	if strings.Count(got, ClearFrame(true)) != 1 || strings.Count(got, "FRAME") != 1 {
		t.Fatalf("interactive frame spam: %q", got)
	}
}

func TestEventLoopRedirectedRendersOnceWithoutANSIOrAutoRefresh(t *testing.T) {
	var out bytes.Buffer
	renders := 0
	err := runEventLoop(context.Background(), strings.NewReader(""), &out, time.Nanosecond, Controller{}, func() string { renders++; return "PLAIN\n" }, TerminalCapabilities{})
	if err != nil {
		t.Fatal(err)
	}
	if renders != 1 || out.String() != "PLAIN\n" {
		t.Fatalf("renders=%d output=%q", renders, out.String())
	}
}

func TestDashboardColorRendererAddsThemeOnlyWhenEnabled(t *testing.T) {
	view := DashboardView{Status: "ONLINE", Message: "ready"}
	colored := RenderDashboardV2(view, true, 100)
	plain := RenderDashboardV2(view, false, 100)
	if !strings.Contains(colored, "\x1b[") {
		t.Fatalf("colored dashboard has no ANSI: %q", colored)
	}
	if strings.Contains(plain, "\x1b[") {
		t.Fatalf("plain dashboard has ANSI: %q", plain)
	}
}
