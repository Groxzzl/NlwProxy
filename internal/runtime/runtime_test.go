package gatewayruntime

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"nlwproxy/internal/config"
	"nlwproxy/internal/gateway"
	"nlwproxy/internal/profiles"
	"nlwproxy/internal/proxymanager"
	"nlwproxy/internal/routing"
)

type mapCredentials map[string]string

func (m mapCredentials) Lookup(name string) (string, error) { return m[name], nil }

type runtimeRoundTripFunc func(*http.Request) (*http.Response, error)

func (f runtimeRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func runtimeAuthorized(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer local")
	return r
}

func testConfig() config.Config {
	cfg := config.Default()
	cfg.Server.Listen = "127.0.0.1:0"
	cfg.Server.LocalTokenEnv = "RUNTIME_LOCAL_TOKEN"
	cfg.Upstreams = []config.Upstream{
		{Name: "direct", BaseURL: "https://direct.example/v1", APIKeyEnv: "RUNTIME_DIRECT_KEY", Enabled: true},
		{Name: "http", BaseURL: "https://http.example/v1", ProxyURL: "http://127.0.0.1:8080", APIKeyEnv: "RUNTIME_HTTP_KEY", Enabled: true},
		{Name: "socks", BaseURL: "https://socks.example/v1", ProxyURL: "socks5://127.0.0.1:1080", APIKeyEnv: "RUNTIME_SOCKS_KEY", Enabled: true},
	}
	return cfg
}

func TestProxyOnlyReturnsStructured503WithoutHealthyProxyAndNeverDialsDirect(t *testing.T) {
	var directDials int
	cfg := testConfig()
	cfg.ProxyOnly = true
	cfg.Upstreams = cfg.Upstreams[:1]
	rt, err := New(Options{
		Profile:     profiles.Profile{ID: "test", Config: cfg},
		Credentials: mapCredentials{"RUNTIME_LOCAL_TOKEN": "local", "RUNTIME_DIRECT_KEY": "upstream"},
		DirectTransport: runtimeRoundTripFunc(func(*http.Request) (*http.Response, error) {
			directDials++
			return nil, errors.New("direct dial must not happen")
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, runtimeAuthorized(http.MethodPost, "/v1/responses", `{}`))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"code":"NO_HEALTHY_PROXY"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if directDials != 0 {
		t.Fatalf("direct upstream dialed %d times", directDials)
	}
}

func TestProxyOnlyBuildsRoutesOnlyFromHealthyProxyManagerEntries(t *testing.T) {
	cfg := testConfig()
	cfg.ProxyOnly = true
	cfg.Upstreams = cfg.Upstreams[:1]
	manager := proxymanager.New(nil)
	manager.Add(proxymanager.ProxyEntry{Host: "127.0.0.1", Port: 1, Scheme: proxymanager.SchemeHTTP, Alive: false})
	healthyID := manager.Add(proxymanager.ProxyEntry{Host: "127.0.0.1", Port: 2, Scheme: proxymanager.SchemeHTTP, Alive: true})
	rt, err := New(Options{Profile: profiles.Profile{ID: "test", Config: cfg}, Credentials: mapCredentials{
		"RUNTIME_LOCAL_TOKEN": "local", "RUNTIME_DIRECT_KEY": "upstream",
	}, ProxyManager: manager})
	if err != nil {
		t.Fatal(err)
	}
	routes := rt.Snapshot().Routes
	if len(routes) != 1 {
		t.Fatalf("routes=%#v", routes)
	}
	if _, ok := routes["direct@"+healthyID]; !ok {
		t.Fatalf("healthy proxy route missing: %#v", routes)
	}
	for _, route := range routes {
		if route.Transport == "direct" {
			t.Fatalf("direct route leaked: %#v", routes)
		}
	}
}

func TestNewBuildsAllTransportModesAndLoadsFallbackCredentials(t *testing.T) {
	for _, name := range []string{"RUNTIME_LOCAL_TOKEN", "RUNTIME_DIRECT_KEY", "RUNTIME_HTTP_KEY", "RUNTIME_SOCKS_KEY"} {
		t.Setenv(name, "")
	}
	profile := profiles.Profile{ID: "test", Name: "Test", Config: testConfig()}
	rt, err := New(Options{Profile: profile, Credentials: mapCredentials{
		"RUNTIME_LOCAL_TOKEN": "local", "RUNTIME_DIRECT_KEY": "direct", "RUNTIME_HTTP_KEY": "http", "RUNTIME_SOCKS_KEY": "socks",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("RUNTIME_LOCAL_TOKEN"); got != "local" {
		t.Fatalf("credential not injected: %q", got)
	}
	snap := rt.Snapshot()
	want := map[string]string{"direct": "direct", "http": "http", "socks": "socks5"}
	if len(snap.Routes) != len(want) {
		t.Fatalf("routes = %#v", snap.Routes)
	}
	for name, mode := range want {
		if got := snap.Routes[name].Transport; got != mode {
			t.Errorf("%s transport = %q, want %q", name, got, mode)
		}
	}
}

func TestNewRejectsMissingCredential(t *testing.T) {
	cfg := testConfig()
	_, err := New(Options{Profile: profiles.Profile{ID: "test", Name: "Test", Config: cfg}, Credentials: mapCredentials{}})
	if err == nil {
		t.Fatal("expected missing credential error")
	}
}

func TestRuntimeLifecycleReadinessAndTelemetry(t *testing.T) {
	cfg := testConfig()
	cfg.Upstreams = cfg.Upstreams[:1]
	rt, err := New(Options{Profile: profiles.Profile{ID: "test", Name: "Test", Config: cfg}, Credentials: mapCredentials{
		"RUNTIME_LOCAL_TOKEN": "local", "RUNTIME_DIRECT_KEY": "direct",
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-rt.Ready():
	case <-time.After(2 * time.Second):
		t.Fatal("runtime not ready")
	}
	if snap := rt.Snapshot(); snap.State != gateway.StateRunning || snap.Listen == "" {
		t.Fatalf("running snapshot = %#v", snap)
	}

	req, _ := http.NewRequest(http.MethodGet, rt.BaseURL()+"/health", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp.StatusCode)
	}

	if err := rt.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if snap := rt.Snapshot(); snap.State != gateway.StateStopped {
		t.Fatalf("stopped snapshot = %#v", snap)
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("second stop: %v", err)
	}
}

func TestStartFailureIsObservableAndRetryable(t *testing.T) {
	cfg := testConfig()
	cfg.Upstreams = cfg.Upstreams[:1]
	first, err := New(Options{Profile: profiles.Profile{ID: "one", Name: "One", Config: cfg}, Credentials: mapCredentials{
		"RUNTIME_LOCAL_TOKEN": "local", "RUNTIME_DIRECT_KEY": "direct",
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := first.Start(ctx); err != nil {
		t.Fatal(err)
	}
	<-first.Ready()

	cfg.Server.Listen = first.Snapshot().Listen
	second, err := New(Options{Profile: profiles.Profile{ID: "two", Name: "Two", Config: cfg}, Credentials: mapCredentials{
		"RUNTIME_LOCAL_TOKEN": "local", "RUNTIME_DIRECT_KEY": "direct",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Start(context.Background()); err == nil {
		t.Fatal("expected occupied-listen failure")
	}
	if snap := second.Snapshot(); snap.State != gateway.StateFailed || snap.Error == "" {
		t.Fatalf("failure snapshot = %#v", snap)
	}
	if err := first.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := second.Start(context.Background()); err != nil {
		t.Fatalf("retry start: %v", err)
	}
	<-second.Ready()
	if err := second.Stop(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestAuthenticatedRuntimeStreamsAndPublishesAccurateTelemetry(t *testing.T) {
	t.Setenv("RUNTIME_LOCAL_TOKEN", "")
	t.Setenv("RUNTIME_DIRECT_KEY", "")
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer upstream-secret" {
			t.Errorf("upstream auth=%q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: first\n\n")
		flusher.Flush()
		time.Sleep(20 * time.Millisecond)
		fmt.Fprint(w, "data: {\"usage\":{\"input_tokens\":7,\"output_tokens\":11,\"total_tokens\":18}}\n\n")
	}))
	defer upstream.Close()
	cfg := testConfig()
	cfg.Upstreams = []config.Upstream{{Name: "mock-stream", BaseURL: upstream.URL, APIKeyEnv: "RUNTIME_DIRECT_KEY", Enabled: true}}
	rt, err := New(Options{Profile: profiles.Profile{ID: "test", Config: cfg}, Credentials: mapCredentials{"RUNTIME_LOCAL_TOKEN": "local", "RUNTIME_DIRECT_KEY": "upstream-secret"}, BaseTransport: upstream.Client().Transport.(*http.Transport)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	<-rt.Ready()
	req, _ := http.NewRequest(http.MethodPost, rt.BaseURL()+"/v1/chat/completions", strings.NewReader(`{"model":"runtime-model","stream":true,"messages":[{"content":"do-not-store"}]}`))
	req.Header.Set("Authorization", "Bearer local")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(resp.Body)
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	active := rt.Snapshot().Metrics
	if active.Active != 1 || len(active.Events) != 1 || active.Events[0].State != "streaming" || active.Events[0].RouteID != "mock-stream" {
		t.Fatalf("streaming snapshot=%+v", active)
	}
	_, _ = io.Copy(io.Discard, reader)
	resp.Body.Close()
	completed := rt.Snapshot().Metrics.Events[0]
	if completed.State != "completed" || completed.RequestedModel != "runtime-model" || completed.InputTokens != 7 || completed.OutputTokens != 11 || completed.TotalTokens != 18 || completed.TTFT <= 0 || completed.Duration < completed.TTFT {
		t.Fatalf("completed=%+v", completed)
	}
}

func TestTransparentModelsAndEventsAreWired(t *testing.T) {
	cfg := testConfig()
	cfg.Upstreams = cfg.Upstreams[:1]
	rt, err := New(Options{Profile: profiles.Profile{ID: "test", Name: "Test", Config: cfg}, Credentials: mapCredentials{
		"RUNTIME_LOCAL_TOKEN": "local", "RUNTIME_DIRECT_KEY": "direct",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if rt.Handler() == nil || rt.Events() == nil || rt.Models() == nil || rt.Selector() == nil {
		t.Fatal("runtime dependencies not exposed")
	}
	_ = routing.Healthy
}
